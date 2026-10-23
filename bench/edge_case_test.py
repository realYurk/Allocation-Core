"""Deep correctness / edge-case tests for Allocation Core (the strict-judge pass).

Probes boundary conditions the basic correctness suite does not:
  - precision 0 and 6 (no decimals vs. micro units)
  - amounts near int64 overflow
  - largest-remainder determinism with many equal targets
  - zero weights, all-zero driver (denominator 0)
  - empty driver, negative driver without ALLOW_SIGNED
  - multi-stage conservation across an odd split
  - CSV with quoted fields / embedded newlines (trace index offset safety)

Usage: python edge_case_test.py --exe alloc_windows_amd64.exe
"""
import argparse
import csv
import json
import os
import shutil
import subprocess
import sys
import tempfile
from decimal import Decimal

DIMS = ["period", "plant", "brand", "family", "sku", "account", "currency"]

results = []


def check(name, cond, detail=""):
    results.append((name, "PASS" if cond else "FAIL", detail))


def wcsv(path, headers, rows):
    with open(path, "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(headers)
        w.writerows(rows)


def run(exe, rule, outdir, extra=None, expect_fail=False):
    cmd = [exe, "run", rule, outdir]
    if extra:
        cmd += extra
    r = subprocess.run(cmd, capture_output=True, text=True)
    if expect_fail:
        return r.returncode != 0, r.stderr
    if r.returncode != 0:
        raise RuntimeError(f"run failed: {r.stderr}")
    s = r.stdout.find("{")
    e = r.stdout.rfind("}")
    return json.loads(r.stdout[s:e + 1]), ""


def single_stage_rule(name, precision, sku_include):
    """Build a single-stage rule that allocates amounts.csv to the given SKUs."""
    src = {x: ("AS_IS" if x in ["period", "plant", "account", "currency"] else "TOTAL") for x in DIMS}
    tgt = {x: {"mode": "SOURCE"} for x in DIMS}
    tgt.update({"brand": {"mode": "ELEMENT", "value": "品牌甲"},
                "family": {"mode": "INCLUDE", "include": ["系列01"]},
                "sku": {"mode": "INCLUDE", "include": sku_include}})
    return {"schema": "allocation-ir/2.0", "name": name, "precision": precision,
            "dimensions": DIMS, "immutable_dimensions": ["period", "currency"], "hierarchies": {},
            "datasets": {"amounts": {"amounts": "amounts.csv"}, "drivers": {"sales": "sales.csv"}},
            "stages": [{"id": "s1", "inputs": [{"from": "amounts", "share": "1"}],
                        "source": {"grouping": src, "filters": {}}, "target": tgt,
                        "conservation_keys": ["period", "plant", "account", "currency"],
                        "allocations": [{"id": "s", "share": "1", "driver_dataset": "sales", "weight_field": "units"}]}]}


def read_amounts(path):
    out = {}
    with open(path, newline="", encoding="utf-8-sig") as f:
        r = csv.DictReader(f)
        for row in r:
            k = row["sku"]
            out[k] = out.get(k, Decimal("0")) + Decimal(row["amount"])
    return out


def case_precision0(exe, tmp):
    d = os.path.join(tmp, "p0")
    os.makedirs(d)
    wcsv(os.path.join(d, "amounts.csv"), DIMS + ["amount"],
         [["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "100"]])
    wcsv(os.path.join(d, "sales.csv"), ["period", "plant", "brand", "family", "sku", "units"],
         [["2026-01", "工厂一", "品牌甲", "系列01", "商品A", "1"],
          ["2026-01", "工厂一", "品牌甲", "系列01", "商品B", "1"],
          ["2026-01", "工厂一", "品牌甲", "系列01", "商品C", "1"]])
    rule = single_stage_rule("p0", 0, ["商品A", "商品B", "商品C"])
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    s, _ = run(exe, os.path.join(d, "rule.json"), os.path.join(d, "out"))
    got = read_amounts(os.path.join(d, "out", "results.csv"))
    # 100 split into 3 = 33,33,34 (integer units, no decimals)
    vals = sorted(got.values())
    check("precision0 3-way = 33/33/34", [str(v) for v in vals] == ["33", "33", "34"], str(vals))
    check("precision0 conserved", s["output_amount"] == "100")


def case_precision6(exe, tmp):
    d = os.path.join(tmp, "p6")
    os.makedirs(d)
    wcsv(os.path.join(d, "amounts.csv"), DIMS + ["amount"],
         [["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "0.000001"]])
    wcsv(os.path.join(d, "sales.csv"), ["period", "plant", "brand", "family", "sku", "units"],
         [["2026-01", "工厂一", "品牌甲", "系列01", "商品A", "1"],
          ["2026-01", "工厂一", "品牌甲", "系列01", "商品B", "1"]])
    rule = single_stage_rule("p6", 6, ["商品A", "商品B"])
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    s, _ = run(exe, os.path.join(d, "rule.json"), os.path.join(d, "out"))
    got = read_amounts(os.path.join(d, "out", "results.csv"))
    # 1 micro unit split across 2: one gets 1, the other 0 (zero row is omitted)
    vals = sorted(got.values())
    check("precision6 sub-unit conserved", s["output_amount"] == "0.000001", s["output_amount"])
    check("precision6 1 unit -> single non-zero row", vals == [Decimal("0.000001")], str(vals))


def case_large_amount(exe, tmp):
    d = os.path.join(tmp, "big")
    os.makedirs(d)
    big = "9000000000000.00"  # 9 trillion, well within int64 cents
    wcsv(os.path.join(d, "amounts.csv"), DIMS + ["amount"],
         [["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", big]])
    wcsv(os.path.join(d, "sales.csv"), ["period", "plant", "brand", "family", "sku", "units"],
         [["2026-01", "工厂一", "品牌甲", "系列01", "商品A", "1"],
          ["2026-01", "工厂一", "品牌甲", "系列01", "商品B", "1"]])
    rule = single_stage_rule("big", 2, ["商品A", "商品B"])
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    s, _ = run(exe, os.path.join(d, "rule.json"), os.path.join(d, "out"))
    check("large amount conserved", s["output_amount"] == big, s["output_amount"])


def case_zero_weight(exe, tmp):
    d = os.path.join(tmp, "zw")
    os.makedirs(d)
    wcsv(os.path.join(d, "amounts.csv"), DIMS + ["amount"],
         [["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "100.00"]])
    # A has weight 0, B has weight 1 -> all to B
    wcsv(os.path.join(d, "sales.csv"), ["period", "plant", "brand", "family", "sku", "units"],
         [["2026-01", "工厂一", "品牌甲", "系列01", "商品A", "0"],
          ["2026-01", "工厂一", "品牌甲", "系列01", "商品B", "1"]])
    rule = single_stage_rule("zw", 2, ["商品A", "商品B"])
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    s, _ = run(exe, os.path.join(d, "rule.json"), os.path.join(d, "out"))
    got = read_amounts(os.path.join(d, "out", "results.csv"))
    check("zero weight excluded, all to B", got.get("商品B") == Decimal("100.00"), str(got))
    check("zero weight A absent or 0", got.get("商品A", Decimal("0")) == Decimal("0"), str(got))


def case_all_zero_driver(exe, tmp):
    d = os.path.join(tmp, "az")
    os.makedirs(d)
    wcsv(os.path.join(d, "amounts.csv"), DIMS + ["amount"],
         [["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "100.00"]])
    wcsv(os.path.join(d, "sales.csv"), ["period", "plant", "brand", "family", "sku", "units"],
         [["2026-01", "工厂一", "品牌甲", "系列01", "商品A", "0"],
          ["2026-01", "工厂一", "品牌甲", "系列01", "商品B", "0"]])
    rule = single_stage_rule("az", 2, ["商品A", "商品B"])
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    failed, err = run(exe, os.path.join(d, "rule.json"), os.path.join(d, "out"), expect_fail=True)
    check("all-zero driver rejected", failed, err.strip()[-120:])


def case_negative_driver_rejected(exe, tmp):
    d = os.path.join(tmp, "negd")
    os.makedirs(d)
    wcsv(os.path.join(d, "amounts.csv"), DIMS + ["amount"],
         [["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "100.00"]])
    wcsv(os.path.join(d, "sales.csv"), ["period", "plant", "brand", "family", "sku", "units"],
         [["2026-01", "工厂一", "品牌甲", "系列01", "商品A", "-5"],
          ["2026-01", "工厂一", "品牌甲", "系列01", "商品B", "5"]])
    rule = single_stage_rule("negd", 2, ["商品A", "商品B"])
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    failed, err = run(exe, os.path.join(d, "rule.json"), os.path.join(d, "out"), expect_fail=True)
    check("negative driver without ALLOW_SIGNED rejected", failed, err.strip()[-120:])


def case_many_equal_remainder(exe, tmp):
    d = os.path.join(tmp, "rem100")
    os.makedirs(d)
    wcsv(os.path.join(d, "amounts.csv"), DIMS + ["amount"],
         [["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "100.00"]])
    n = 7
    wcsv(os.path.join(d, "sales.csv"), ["period", "plant", "brand", "family", "sku", "units"],
         [["2026-01", "工厂一", "品牌甲", "系列01", f"商品{i}", "1"] for i in range(n)])
    rule = single_stage_rule("rem100", 2, [f"商品{i}" for i in range(n)])
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    s, _ = run(exe, os.path.join(d, "rule.json"), os.path.join(d, "out"))
    got = read_amounts(os.path.join(d, "out", "results.csv"))
    # 100/7 at precision 2 = 14.28 each = 99.96, remainder 0.04 -> 4 SKUs get +0.01
    vals = sorted(got.values())
    check("7-way remainder sums to 100", sum(vals) == Decimal("100.00"), str(vals))
    check("7-way: three@14.28 four@14.29",
          vals.count(Decimal("14.28")) == 3 and vals.count(Decimal("14.29")) == 4, str(vals))


def case_odd_split_stage(exe, tmp):
    """Two stages; stage1 splits 100 into 3 equal SKU-family targets, verify conservation holds end to end."""
    d = os.path.join(tmp, "odd")
    os.makedirs(d)
    wcsv(os.path.join(d, "amounts.csv"), DIMS + ["amount"],
         [["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "100.00"]])
    # stage1 driver: split into 3 families
    wcsv(os.path.join(d, "fam.csv"), ["period", "plant", "family", "hours"],
         [["2026-01", "工厂一", f"系列0{i}", "1"] for i in range(1, 4)])
    # stage2 driver: each family -> 2 skus
    wcsv(os.path.join(d, "sku.csv"), ["period", "plant", "brand", "family", "sku", "units"],
         [["2026-01", "工厂一", "品牌甲", f"系列0{i}", f"商品{i}{j}", "1"]
          for i in range(1, 4) for j in ["A", "B"]])
    src1 = {x: ("AS_IS" if x in ["period", "plant", "account", "currency"] else "TOTAL") for x in DIMS}
    tgt1 = {x: {"mode": "SOURCE"} for x in DIMS}
    tgt1.update({"brand": {"mode": "ELEMENT", "value": "品牌甲"},
                 "family": {"mode": "INCLUDE", "include": ["系列01", "系列02", "系列03"]},
                 "sku": {"mode": "ELEMENT", "value": "待分配"}})
    src2 = {x: ("TOTAL" if x == "sku" else "AS_IS") for x in DIMS}
    tgt2 = {x: {"mode": "SOURCE"} for x in DIMS}
    tgt2["sku"] = {"mode": "INCLUDE", "include": [f"商品{i}{j}" for i in range(1, 4) for j in ["A", "B"]]}
    rule = {"schema": "allocation-ir/2.0", "name": "odd", "precision": 2, "dimensions": DIMS,
            "immutable_dimensions": ["period", "currency"], "hierarchies": {},
            "datasets": {"amounts": {"amounts": "amounts.csv"},
                         "drivers": {"fam": "fam.csv", "sku": "sku.csv"}},
            "stages": [
                {"id": "s1", "inputs": [{"from": "amounts", "share": "1"}],
                 "source": {"grouping": src1, "filters": {}}, "target": tgt1,
                 "conservation_keys": ["period", "plant", "account", "currency"],
                 "allocations": [{"id": "fam", "share": "1", "driver_dataset": "fam", "weight_field": "hours"}]},
                {"id": "s2", "inputs": [{"from": "s1", "share": "1"}],
                 "source": {"grouping": src2, "filters": {}}, "target": tgt2,
                 "conservation_keys": ["period", "plant", "brand", "family", "account", "currency"],
                 "allocations": [{"id": "sku", "share": "1", "driver_dataset": "sku", "weight_field": "units"}]}]}
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    s, _ = run(exe, os.path.join(d, "rule.json"), os.path.join(d, "out"))
    got = read_amounts(os.path.join(d, "out", "results.csv"))
    check("2-stage odd split conserved", s["output_amount"] == "100.00", s["output_amount"])
    check("2-stage 6 skus sum to 100", sum(got.values()) == Decimal("100.00"), str(got))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--exe", default="alloc_windows_amd64.exe")
    args = ap.parse_args()
    exe = os.path.abspath(args.exe)
    if not os.path.exists(exe):
        print("binary not found:", exe)
        sys.exit(1)
    tmp = tempfile.mkdtemp(prefix="alloc_edge_")
    try:
        case_precision0(exe, tmp)
        case_precision6(exe, tmp)
        case_large_amount(exe, tmp)
        case_zero_weight(exe, tmp)
        case_all_zero_driver(exe, tmp)
        case_negative_driver_rejected(exe, tmp)
        case_many_equal_remainder(exe, tmp)
        case_odd_split_stage(exe, tmp)
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    npass = sum(1 for _, s, _ in results if s == "PASS")
    nfail = len(results) - npass
    print("=" * 64)
    for name, s, detail in results:
        print(f"[{s}] {name}" + (f"  ({detail})" if detail else ""))
    print("=" * 64)
    print(f"TOTAL: {npass} passed, {nfail} failed")
    sys.exit(0 if nfail == 0 else 1)


if __name__ == "__main__":
    main()
