"""Generate synthetic large-scale allocation data for stress testing.

No third-party dependencies. All data is fabricated.

Terminal rows ~= periods * accounts * skus (for the standard two-stage layout),
so tune the three knobs to hit your target scale.

Usage:
  python gen_stress.py OUTDIR --periods 12 --accounts 100 --skus 5000
  python gen_stress.py OUTDIR --periods 12 --accounts 100 --skus 10000 --multi-factor
"""
import argparse
import csv
import json
import os
import sys

DIMS = ["period", "plant", "brand", "family", "sku", "account", "currency"]


def parse_args(argv):
    p = argparse.ArgumentParser()
    p.add_argument("outdir")
    p.add_argument("--periods", type=int, default=12)
    p.add_argument("--accounts", type=int, default=100)
    p.add_argument("--skus", type=int, default=5000)
    p.add_argument("--plants", type=int, default=1)
    p.add_argument("--families", type=int, default=2)
    p.add_argument("--multi-factor", action="store_true",
                   help="split stage2 across two drivers (60% sales, 40% orders)")
    p.add_argument("--amount", type=str, default="1000.00",
                   help="amount per source row")
    p.add_argument("--precision", type=int, default=2)
    return p.parse_args(argv)


def write_csv(path, headers, rows):
    with open(path, "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(headers)
        w.writerows(rows)


def main(argv):
    a = parse_args(argv)
    root = a.outdir
    os.makedirs(root, exist_ok=True)
    periods = [f"2026-{m:02d}" for m in range(1, a.periods + 1)]
    families = [f"系列{chr(0x4E00 + i)}" for i in range(a.families)]
    # families use 甲乙丙...; keep it simple with numbered names to avoid CJK edge cases
    families = [f"系列{i:02d}" for i in range(1, a.families + 1)]
    sku_names = [f"商品{i:06d}" for i in range(1, a.skus + 1)]
    plants = [f"工厂{i:02d}" for i in range(1, a.plants + 1)]

    # --- amounts.csv: periods * accounts * plants source rows ---
    amount_rows = []
    for period in periods:
        for plant in plants:
            for i in range(1, a.accounts + 1):
                amount_rows.append([period, plant, "待分配品牌", "待分配系列",
                                    "待分配", f"科目{i:03d}", "CNY", a.amount])
    write_csv(os.path.join(root, "amounts.csv"),
              DIMS + ["amount"], amount_rows)

    # --- labor.csv: stage1 driver, splits account -> family ---
    labor_rows = []
    for period in periods:
        for plant in plants:
            for fam in families:
                labor_rows.append([period, plant, fam, "100"])
    write_csv(os.path.join(root, "labor.csv"),
              ["period", "plant", "family", "hours"], labor_rows)

    # --- sales.csv: stage2 driver, splits family -> sku ---
    sales_rows = []
    for period in periods:
        for plant in plants:
            for i in range(1, a.skus + 1):
                fam = families[(i - 1) % len(families)]
                sales_rows.append([period, plant, "品牌甲", fam, sku_names[i - 1], "1"])
    write_csv(os.path.join(root, "sales.csv"),
              ["period", "plant", "brand", "family", "sku", "units"], sales_rows)

    source1 = {d: ("AS_IS" if d in ["period", "plant", "account", "currency"] else "TOTAL")
               for d in DIMS}
    target1 = {d: {"mode": "SOURCE"} for d in DIMS}
    target1.update({
        "brand": {"mode": "ELEMENT", "value": "品牌甲"},
        "family": {"mode": "INCLUDE", "include": families},
        "sku": {"mode": "ELEMENT", "value": "待分配"},
    })
    source2 = {d: ("TOTAL" if d == "sku" else "AS_IS") for d in DIMS}
    target2 = {d: {"mode": "SOURCE"} for d in DIMS}
    target2["sku"] = {"mode": "INCLUDE", "include": sku_names}

    stage2_allocs = [{"id": "sales", "share": "1",
                      "driver_dataset": "sales", "weight_field": "units"}]
    drivers = {"labor": "labor.csv", "sales": "sales.csv"}

    if a.multi_factor:
        # orders.csv: a second stage2 driver with a different weight.
        orders_rows = []
        for period in periods:
            for plant in plants:
                for i in range(1, a.skus + 1):
                    fam = families[(i - 1) % len(families)]
                    orders_rows.append([period, plant, "品牌甲", fam, sku_names[i - 1], "2"])
        write_csv(os.path.join(root, "orders.csv"),
                  ["period", "plant", "brand", "family", "sku", "orders"], orders_rows)
        drivers["orders"] = "orders.csv"
        stage2_allocs = [
            {"id": "sales", "share": "0.6", "driver_dataset": "sales", "weight_field": "units"},
            {"id": "orders", "share": "0.4", "driver_dataset": "orders", "weight_field": "orders"},
        ]

    rule = {
        "schema": "allocation-ir/2.0",
        "name": f"Synthetic stress {a.periods} periods x {a.accounts} accounts x {a.skus} skus",
        "precision": a.precision,
        "dimensions": DIMS,
        "immutable_dimensions": ["period", "currency"],
        "hierarchies": {},
        "datasets": {"amounts": {"amounts": "amounts.csv"}, "drivers": drivers},
        "stages": [
            {
                "id": "stage1",
                "inputs": [{"from": "amounts", "share": "1"}],
                "source": {"grouping": source1, "filters": {}},
                "target": target1,
                "conservation_keys": ["period", "plant", "account", "currency"],
                "allocations": [{"id": "labor", "share": "1",
                                 "driver_dataset": "labor", "weight_field": "hours"}],
            },
            {
                "id": "stage2",
                "inputs": [{"from": "stage1", "share": "1"}],
                "source": {"grouping": source2, "filters": {}},
                "target": target2,
                "conservation_keys": ["period", "plant", "brand", "family", "account", "currency"],
                "allocations": stage2_allocs,
            },
        ],
    }
    with open(os.path.join(root, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, indent=2, ensure_ascii=False)

    pools = a.periods * a.accounts * a.plants
    terminal = pools * a.skus
    print(f"Generated {root}")
    print(f"  source rows : {len(amount_rows):,}")
    print(f"  driver rows : labor={len(labor_rows):,} sales={len(sales_rows):,}")
    print(f"  pools       : {pools:,}")
    print(f"  terminal SKU rows ~= {terminal:,}")
    print(f"  multi-factor: {a.multi_factor}")


if __name__ == "__main__":
    main(sys.argv[1:])
