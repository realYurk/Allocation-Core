"""Correctness / accuracy test harness for Allocation Core.

Builds small cases with KNOWN answers, runs the engine, and asserts the
results are exact. Also verifies byte-for-byte reproducibility across runs.

No third-party dependencies. Run with the built Windows binary:

  python correctness_test.py --exe alloc_windows_amd64.exe
"""
import argparse
import csv
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tempfile

DIMS = ["period", "plant", "brand", "family", "sku", "account", "currency"]


def write_csv(path, headers, rows):
    with open(path, "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(headers)
        w.writerows(rows)


def run(exe, rule, outdir, extra=None):
    cmd = [exe, "run", rule, outdir]
    if extra:
        cmd += extra
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        raise RuntimeError(f"run failed: {r.stderr}")
    # summary JSON is pretty-printed (multi-line); find the first { .. last }
    start = r.stdout.find("{")
    end = r.stdout.rfind("}")
    if start < 0 or end < 0:
        raise RuntimeError(f"no JSON in stdout: {r.stdout[:200]}")
    summary = json.loads(r.stdout[start:end + 1])
    return summary


def read_csv(path):
    with open(path, newline="", encoding="utf-8-sig") as f:
        return list(csv.reader(f))


def file_sha(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def load_results(results_path):
    """Sum amounts by SKU (a SKU can span multiple rows across drivers)."""
    rows = read_csv(results_path)
    header = rows[0]
    out = {}
    from decimal import Decimal
    for r in rows[1:]:
        d = dict(zip(header, r))
        sku = d["sku"]
        out[sku] = (out.get(sku, Decimal("0")) + Decimal(d["amount"])).quantize(Decimal("0.01"))
    return {k: str(v) for k, v in out.items()}


PASS, FAIL = "PASS", "FAIL"
results = []


def check(name, cond, detail=""):
    results.append((name, PASS if cond else FAIL, detail))


def case_exact_split(exe, tmp):
    """Two skus, weight 3:1, amount 100.00 -> 75.00 and 25.00 exactly."""
    d = os.path.join(tmp, "exact")
    os.makedirs(d)
    write_csv(os.path.join(d, "amounts.csv"), DIMS + ["amount"],
              [["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "100.00"]])
    write_csv(os.path.join(d, "driver.csv"),
              ["period", "plant", "brand", "family", "sku", "units"],
              [["2026-01", "工厂一", "品牌甲", "系列01", "商品A", "3"],
               ["2026-01", "工厂一", "品牌甲", "系列01", "商品B", "1"]])
    src = {x: ("AS_IS" if x in ["period", "plant", "account", "currency"] else "TOTAL") for x in DIMS}
    tgt = {x: {"mode": "SOURCE"} for x in DIMS}
    tgt.update({"brand": {"mode": "ELEMENT", "value": "品牌甲"},
                "family": {"mode": "INCLUDE", "include": ["系列01"]},
                "sku": {"mode": "INCLUDE", "include": ["商品A", "商品B"]}})
    rule = {"schema": "allocation-ir/2.0", "name": "exact", "precision": 2,
            "dimensions": DIMS, "immutable_dimensions": ["period", "currency"],
            "hierarchies": {},
            "datasets": {"amounts": {"amounts": "amounts.csv"}, "drivers": {"d": "driver.csv"}},
            "stages": [{"id": "s1", "inputs": [{"from": "amounts", "share": "1"}],
                        "source": {"grouping": src, "filters": {}}, "target": tgt,
                        "conservation_keys": ["period", "plant", "account", "currency"],
                        "allocations": [{"id": "a", "share": "1", "driver_dataset": "d", "weight_field": "units"}]}]}
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    s = run(exe, os.path.join(d, "rule.json"), os.path.join(d, "out"))
    got = load_results(os.path.join(d, "out", "results.csv"))
    check("exact 3:1 split A=75.00", got.get("商品A") == "75.00", f"A={got.get('商品A')}")
    check("exact 3:1 split B=25.00", got.get("商品B") == "25.00", f"B={got.get('商品B')}")
    check("total conserved 100.00", s["output_amount"] == "100.00", s["output_amount"])


def case_largest_remainder(exe, tmp):
    """100.00 split across 3 equal weights -> 33.33 / 33.33 / 33.34 (largest remainder)."""
    d = os.path.join(tmp, "rem")
    os.makedirs(d)
    write_csv(os.path.join(d, "amounts.csv"), DIMS + ["amount"],
              [["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "100.00"]])
    write_csv(os.path.join(d, "driver.csv"),
              ["period", "plant", "brand", "family", "sku", "units"],
              [["2026-01", "工厂一", "品牌甲", "系列01", f"商品{i}", "1"] for i in ["A", "B", "C"]])
    src = {x: ("AS_IS" if x in ["period", "plant", "account", "currency"] else "TOTAL") for x in DIMS}
    tgt = {x: {"mode": "SOURCE"} for x in DIMS}
    tgt.update({"brand": {"mode": "ELEMENT", "value": "品牌甲"},
                "family": {"mode": "INCLUDE", "include": ["系列01"]},
                "sku": {"mode": "INCLUDE", "include": ["商品A", "商品B", "商品C"]}})
    rule = {"schema": "allocation-ir/2.0", "name": "rem", "precision": 2,
            "dimensions": DIMS, "immutable_dimensions": ["period", "currency"],
            "hierarchies": {},
            "datasets": {"amounts": {"amounts": "amounts.csv"}, "drivers": {"d": "driver.csv"}},
            "stages": [{"id": "s1", "inputs": [{"from": "amounts", "share": "1"}],
                        "source": {"grouping": src, "filters": {}}, "target": tgt,
                        "conservation_keys": ["period", "plant", "account", "currency"],
                        "allocations": [{"id": "a", "share": "1", "driver_dataset": "d", "weight_field": "units"}]}]}
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    s = run(exe, os.path.join(d, "rule.json"), os.path.join(d, "out"))
    got = load_results(os.path.join(d, "out", "results.csv"))
    vals = sorted(got.values())
    check("3-way split sums to 100.00", vals == ["33.33", "33.33", "33.34"], str(vals))
    check("largest remainder gives 33.34 once", vals.count("33.34") == 1, str(vals))


def case_multi_factor(exe, tmp):
    """100.00 split 60/40 across two drivers, each 1:1 -> 30/30 and 20/20."""
    d = os.path.join(tmp, "mf")
    os.makedirs(d)
    write_csv(os.path.join(d, "amounts.csv"), DIMS + ["amount"],
              [["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "100.00"]])
    write_csv(os.path.join(d, "sales.csv"),
              ["period", "plant", "brand", "family", "sku", "units"],
              [["2026-01", "工厂一", "品牌甲", "系列01", "商品A", "1"],
               ["2026-01", "工厂一", "品牌甲", "系列01", "商品B", "1"]])
    write_csv(os.path.join(d, "orders.csv"),
              ["period", "plant", "brand", "family", "sku", "orders"],
              [["2026-01", "工厂一", "品牌甲", "系列01", "商品A", "1"],
               ["2026-01", "工厂一", "品牌甲", "系列01", "商品B", "1"]])
    src = {x: ("AS_IS" if x in ["period", "plant", "account", "currency"] else "TOTAL") for x in DIMS}
    tgt = {x: {"mode": "SOURCE"} for x in DIMS}
    tgt.update({"brand": {"mode": "ELEMENT", "value": "品牌甲"},
                "family": {"mode": "INCLUDE", "include": ["系列01"]},
                "sku": {"mode": "INCLUDE", "include": ["商品A", "商品B"]}})
    rule = {"schema": "allocation-ir/2.0", "name": "mf", "precision": 2,
            "dimensions": DIMS, "immutable_dimensions": ["period", "currency"],
            "hierarchies": {},
            "datasets": {"amounts": {"amounts": "amounts.csv"},
                         "drivers": {"s": "sales.csv", "o": "orders.csv"}},
            "stages": [{"id": "s1", "inputs": [{"from": "amounts", "share": "1"}],
                        "source": {"grouping": src, "filters": {}}, "target": tgt,
                        "conservation_keys": ["period", "plant", "account", "currency"],
                        "allocations": [
                            {"id": "sales", "share": "0.6", "driver_dataset": "s", "weight_field": "units"},
                            {"id": "orders", "share": "0.4", "driver_dataset": "o", "weight_field": "orders"}]}]}
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    s = run(exe, os.path.join(d, "rule.json"), os.path.join(d, "out"))
    got = load_results(os.path.join(d, "out", "results.csv"))
    check("multi-factor A=50.00", got.get("商品A") == "50.00", f"A={got.get('商品A')}")
    check("multi-factor B=50.00", got.get("商品B") == "50.00", f"B={got.get('商品B')}")
    check("multi-factor total 100.00", s["output_amount"] == "100.00")


def case_reproducibility(exe, tmp):
    """Same input twice -> byte-identical outputs."""
    d = os.path.join(tmp, "repro")
    os.makedirs(d)
    write_csv(os.path.join(d, "amounts.csv"), DIMS + ["amount"],
              [["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", f"科目{i:03d}", "CNY", "1000.00"]
               for i in range(1, 11)])
    write_csv(os.path.join(d, "driver.csv"),
              ["period", "plant", "brand", "family", "sku", "units"],
              [["2026-01", "工厂一", "品牌甲", "系列01", f"商品{i:03d}", "1"] for i in range(1, 101)])
    src = {x: ("AS_IS" if x in ["period", "plant", "account", "currency"] else "TOTAL") for x in DIMS}
    tgt = {x: {"mode": "SOURCE"} for x in DIMS}
    tgt.update({"brand": {"mode": "ELEMENT", "value": "品牌甲"},
                "family": {"mode": "INCLUDE", "include": ["系列01"]},
                "sku": {"mode": "INCLUDE", "include": [f"商品{i:03d}" for i in range(1, 101)]}})
    rule = {"schema": "allocation-ir/2.0", "name": "repro", "precision": 2,
            "dimensions": DIMS, "immutable_dimensions": ["period", "currency"],
            "hierarchies": {},
            "datasets": {"amounts": {"amounts": "amounts.csv"}, "drivers": {"d": "driver.csv"}},
            "stages": [{"id": "s1", "inputs": [{"from": "amounts", "share": "1"}],
                        "source": {"grouping": src, "filters": {}}, "target": tgt,
                        "conservation_keys": ["period", "plant", "account", "currency"],
                        "allocations": [{"id": "a", "share": "1", "driver_dataset": "d", "weight_field": "units"}]}]}
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    out1 = os.path.join(d, "out1")
    out2 = os.path.join(d, "out2")
    run(exe, os.path.join(d, "rule.json"), out1, ["--workers", "1"])
    run(exe, os.path.join(d, "rule.json"), out2, ["--workers", "4"])
    same = True
    for name in ["results.csv", "ledger.csv", "reconciliation.csv"]:
        if file_sha(os.path.join(out1, name)) != file_sha(os.path.join(out2, name)):
            same = False
            break
    check("reproducible across worker counts", same)


def case_negative_amount(exe, tmp):
    """Negative source amount is conserved with sign."""
    d = os.path.join(tmp, "neg")
    os.makedirs(d)
    write_csv(os.path.join(d, "amounts.csv"), DIMS + ["amount"],
              [["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "-100.00"]])
    write_csv(os.path.join(d, "driver.csv"),
              ["period", "plant", "brand", "family", "sku", "units"],
              [["2026-01", "工厂一", "品牌甲", "系列01", "商品A", "1"],
               ["2026-01", "工厂一", "品牌甲", "系列01", "商品B", "1"]])
    src = {x: ("AS_IS" if x in ["period", "plant", "account", "currency"] else "TOTAL") for x in DIMS}
    tgt = {x: {"mode": "SOURCE"} for x in DIMS}
    tgt.update({"brand": {"mode": "ELEMENT", "value": "品牌甲"},
                "family": {"mode": "INCLUDE", "include": ["系列01"]},
                "sku": {"mode": "INCLUDE", "include": ["商品A", "商品B"]}})
    rule = {"schema": "allocation-ir/2.0", "name": "neg", "precision": 2,
            "dimensions": DIMS, "immutable_dimensions": ["period", "currency"],
            "hierarchies": {},
            "datasets": {"amounts": {"amounts": "amounts.csv"}, "drivers": {"d": "driver.csv"}},
            "stages": [{"id": "s1", "inputs": [{"from": "amounts", "share": "1"}],
                        "source": {"grouping": src, "filters": {}}, "target": tgt,
                        "conservation_keys": ["period", "plant", "account", "currency"],
                        "allocations": [{"id": "a", "share": "1", "driver_dataset": "d", "weight_field": "units"}]}]}
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    s = run(exe, os.path.join(d, "rule.json"), os.path.join(d, "out"))
    got = load_results(os.path.join(d, "out", "results.csv"))
    check("negative amount conserved", s["output_amount"] == "-100.00", s["output_amount"])
    check("negative split -50/-50",
          got.get("商品A") == "-50.00" and got.get("商品B") == "-50.00",
          f"{got.get('商品A')},{got.get('商品B')}")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--exe", default="alloc_windows_amd64.exe")
    args = ap.parse_args()
    exe = os.path.abspath(args.exe)
    if not os.path.exists(exe):
        print("binary not found:", exe)
        sys.exit(1)
    tmp = tempfile.mkdtemp(prefix="alloc_correctness_")
    try:
        case_exact_split(exe, tmp)
        case_largest_remainder(exe, tmp)
        case_multi_factor(exe, tmp)
        case_reproducibility(exe, tmp)
        case_negative_amount(exe, tmp)
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    npass = sum(1 for _, s, _ in results if s == PASS)
    nfail = len(results) - npass
    print("=" * 60)
    for name, s, detail in results:
        mark = "PASS" if s == PASS else "FAIL"
        print(f"[{mark}] {name}" + (f"  ({detail})" if detail else ""))
    print("=" * 60)
    print(f"TOTAL: {npass} passed, {nfail} failed")
    sys.exit(0 if nfail == 0 else 1)


if __name__ == "__main__":
    main()
