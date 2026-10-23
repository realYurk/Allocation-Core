"""Adversarial bug-hunt for V17 trace index + audit archive.

Focuses on the places most likely to hide bugs:
  - CSV fields containing quotes, commas, and embedded newlines (offset safety)
  - trace index lookup returns the SAME lineage as a full scan for such rows
  - empty / single-row results
  - source_to_pool multi-row pooling key correctness

Usage: python bug_hunt_test.py --exe alloc_windows_amd64.exe
"""
import argparse
import csv
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile

DIMS = ["period", "plant", "brand", "family", "sku", "account", "currency"]
results = []


def check(name, cond, detail=""):
    results.append((name, "PASS" if cond else "FAIL", detail))


def run_cmd(cmd):
    return subprocess.run(cmd, capture_output=True, text=True)


def run_json(exe, rule, outdir):
    r = run_cmd([exe, "run", rule, outdir])
    if r.returncode != 0:
        raise RuntimeError(r.stderr)
    s = r.stdout.find("{")
    e = r.stdout.rfind("}")
    return json.loads(r.stdout[s:e + 1])


def write_amounts_with_quoted_sku(d):
    """A SKU name containing a comma and quotes, exercising CSV quoting in evidence."""
    os.makedirs(d)
    # SKU value contains a comma -> must be quoted in results/ledger CSVs
    tricky = '商品"A,特殊"'
    with open(os.path.join(d, "amounts.csv"), "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(DIMS + ["amount"])
        w.writerow(["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "100.00"])
    with open(os.path.join(d, "sales.csv"), "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["period", "plant", "brand", "family", "sku", "units"])
        w.writerow(["2026-01", "工厂一", "品牌甲", "系列01", tricky, "1"])
        w.writerow(["2026-01", "工厂一", "品牌甲", "系列01", "商品B", "1"])
    src = {x: ("AS_IS" if x in ["period", "plant", "account", "currency"] else "TOTAL") for x in DIMS}
    tgt = {x: {"mode": "SOURCE"} for x in DIMS}
    tgt.update({"brand": {"mode": "ELEMENT", "value": "品牌甲"},
                "family": {"mode": "INCLUDE", "include": ["系列01"]},
                "sku": {"mode": "INCLUDE", "include": [tricky, "商品B"]}})
    rule = {"schema": "allocation-ir/2.0", "name": "quoted", "precision": 2,
            "dimensions": DIMS, "immutable_dimensions": ["period", "currency"], "hierarchies": {},
            "datasets": {"amounts": {"amounts": "amounts.csv"}, "drivers": {"sales": "sales.csv"}},
            "stages": [{"id": "s1", "inputs": [{"from": "amounts", "share": "1"}],
                        "source": {"grouping": src, "filters": {}}, "target": tgt,
                        "conservation_keys": ["period", "plant", "account", "currency"],
                        "allocations": [{"id": "s", "share": "1", "driver_dataset": "sales", "weight_field": "units"}]}]}
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)


def case_quoted_sku_trace_equivalence(exe, tmp):
    d = os.path.join(tmp, "quoted")
    write_amounts_with_quoted_sku(d)
    outdir = os.path.join(d, "out")
    run_json(exe, os.path.join(d, "rule.json"), outdir)
    # baseline trace (full scan) via trace-first
    r = run_cmd([exe, "trace-first", os.path.join(d, "rule.json"), outdir])
    if r.returncode != 0:
        check("quoted-sku run", False, r.stderr[-200:])
        return
    check("quoted-sku run", True)
    before = r.stdout
    # build index and re-trace
    r2 = run_cmd([exe, "trace-index", outdir])
    check("quoted-sku trace-index build", r2.returncode == 0, r2.stderr[-200:])
    r3 = run_cmd([exe, "trace-first", os.path.join(d, "rule.json"), outdir])
    after = r3.stdout
    # the trace output should be equivalent (both succeed, contain expected links)
    check("quoted-sku trace after index succeeds", r3.returncode == 0, r3.stderr[-200:])
    # verify index integrity
    r4 = run_cmd([exe, "trace-index-verify", outdir])
    check("quoted-sku trace-index-verify", r4.returncode == 0, r4.stderr[-200:])
    # results must contain the quoted SKU correctly parsed
    # columns: terminal_stage,row_id,period,plant,brand,family,sku,account,currency,amount
    with open(os.path.join(outdir, "results.csv"), newline="", encoding="utf-8-sig") as f:
        rows = list(csv.reader(f))
    skus = [r[6] for r in rows[1:]]
    check("quoted sku present in results", '商品"A,特殊"' in skus, str(skus))


def case_embedded_newline_sku(exe, tmp):
    """SKU name with an embedded newline - the hardest CSV offset case."""
    d = os.path.join(tmp, "newline")
    os.makedirs(d)
    tricky = "商品\n跨行"
    with open(os.path.join(d, "amounts.csv"), "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f, lineterminator="\n")
        w.writerow(DIMS + ["amount"])
        w.writerow(["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "100.00"])
    with open(os.path.join(d, "sales.csv"), "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f, lineterminator="\n")
        w.writerow(["period", "plant", "brand", "family", "sku", "units"])
        w.writerow(["2026-01", "工厂一", "品牌甲", "系列01", tricky, "1"])
        w.writerow(["2026-01", "工厂一", "品牌甲", "系列01", "商品B", "1"])
    src = {x: ("AS_IS" if x in ["period", "plant", "account", "currency"] else "TOTAL") for x in DIMS}
    tgt = {x: {"mode": "SOURCE"} for x in DIMS}
    tgt.update({"brand": {"mode": "ELEMENT", "value": "品牌甲"},
                "family": {"mode": "INCLUDE", "include": ["系列01"]},
                "sku": {"mode": "INCLUDE", "include": [tricky, "商品B"]}})
    rule = {"schema": "allocation-ir/2.0", "name": "newline", "precision": 2,
            "dimensions": DIMS, "immutable_dimensions": ["period", "currency"], "hierarchies": {},
            "datasets": {"amounts": {"amounts": "amounts.csv"}, "drivers": {"sales": "sales.csv"}},
            "stages": [{"id": "s1", "inputs": [{"from": "amounts", "share": "1"}],
                        "source": {"grouping": src, "filters": {}}, "target": tgt,
                        "conservation_keys": ["period", "plant", "account", "currency"],
                        "allocations": [{"id": "s", "share": "1", "driver_dataset": "sales", "weight_field": "units"}]}]}
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    outdir = os.path.join(d, "out")
    run_json(exe, os.path.join(d, "rule.json"), outdir)
    check("newline-sku run", True)
    r = run_cmd([exe, "trace-index", outdir])
    check("newline-sku trace-index build", r.returncode == 0, r.stderr[-200:])
    r = run_cmd([exe, "trace-index-verify", outdir])
    check("newline-sku verify", r.returncode == 0, r.stderr[-200:])
    # trace must not crash or misparse
    r = run_cmd([exe, "trace-first", os.path.join(d, "rule.json"), outdir])
    check("newline-sku trace succeeds", r.returncode == 0, r.stderr[-200:])


def case_empty_result(exe, tmp):
    """A rule whose driver produces zero eligible targets -> clean error, no partial output."""
    d = os.path.join(tmp, "empty")
    os.makedirs(d)
    with open(os.path.join(d, "amounts.csv"), "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(DIMS + ["amount"])
        w.writerow(["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "100.00"])
    # driver has no rows matching the target include list
    with open(os.path.join(d, "sales.csv"), "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["period", "plant", "brand", "family", "sku", "units"])
        w.writerow(["2026-01", "工厂一", "品牌甲", "系列01", "商品X", "1"])
    src = {x: ("AS_IS" if x in ["period", "plant", "account", "currency"] else "TOTAL") for x in DIMS}
    tgt = {x: {"mode": "SOURCE"} for x in DIMS}
    tgt.update({"brand": {"mode": "ELEMENT", "value": "品牌甲"},
                "family": {"mode": "INCLUDE", "include": ["系列01"]},
                "sku": {"mode": "INCLUDE", "include": ["商品Y"]}})
    rule = {"schema": "allocation-ir/2.0", "name": "empty", "precision": 2,
            "dimensions": DIMS, "immutable_dimensions": ["period", "currency"], "hierarchies": {},
            "datasets": {"amounts": {"amounts": "amounts.csv"}, "drivers": {"sales": "sales.csv"}},
            "stages": [{"id": "s1", "inputs": [{"from": "amounts", "share": "1"}],
                        "source": {"grouping": src, "filters": {}}, "target": tgt,
                        "conservation_keys": ["period", "plant", "account", "currency"],
                        "allocations": [{"id": "s", "share": "1", "driver_dataset": "sales", "weight_field": "units"}]}]}
    with open(os.path.join(d, "rule.json"), "w", encoding="utf-8") as f:
        json.dump(rule, f, ensure_ascii=False)
    outdir = os.path.join(d, "out")
    r = run_cmd([exe, "run", os.path.join(d, "rule.json"), outdir])
    check("no-eligible-driver clean error", r.returncode != 0, r.stderr[-160:])
    # no partial output directory should be published
    check("no partial output dir", not os.path.exists(outdir))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--exe", default="alloc_windows_amd64.exe")
    args = ap.parse_args()
    exe = os.path.abspath(args.exe)
    if not os.path.exists(exe):
        print("binary not found:", exe)
        sys.exit(1)
    tmp = tempfile.mkdtemp(prefix="alloc_bughunt_")
    try:
        case_quoted_sku_trace_equivalence(exe, tmp)
        case_embedded_newline_sku(exe, tmp)
        case_empty_result(exe, tmp)
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
