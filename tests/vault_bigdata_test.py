#!/usr/bin/env python3
"""V20 vault 在大数据下的导出与归档性能测试。

构造 N 个 SKU 的制造业两步分摊（复用 manufacturing 规则结构），
跑出约 N 行结果，然后测：
  1. vault add 压缩/去重耗时与体积
  2. vault export-db 吞吐（行/秒）
  3. vault restore 无损耗时
"""
import csv
import json
import os
import subprocess
import time
import tempfile

EXE = os.path.abspath("core/alloc_windows_amd64.exe")


def run(cmd, **kw):
    return subprocess.run(cmd, capture_output=True, text=True, **kw)


def main(nskus=100000):
    root = tempfile.mkdtemp(prefix="vaultbig_")
    d = os.path.join(root, "data")
    os.makedirs(d)

    # 读取原始规则和驱动，替换 amounts/sales 生成放大版
    base = "core/examples/manufacturing"
    rule = json.load(open(os.path.join(base, "rule.json"), encoding="utf-8"))

    # 生成 amounts.csv：12 期 × 10 科目（120 个金额池）
    amounts = []
    for p in range(1, 13):
        period = f"2026-{p:02d}"
        for a in range(1, 11):
            account = f"科目{a:03d}"
            amounts.append([period, "工厂一", "待分配品牌", "待分配系列", "待分配", account, "CNY", "10000.00"])
    with open(os.path.join(d, "amounts.csv"), "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["period", "plant", "brand", "family", "sku", "account", "currency", "amount"])
        w.writerows(amounts)

    # 生成 sales.csv：12 期 × 每个 SKU 一行（family 用规则里的"系列甲"）
    sku_list = [f"商品{i}" for i in range(nskus)]
    sales = []
    for p in range(1, 13):
        for i in range(nskus):
            sales.append([f"2026-{p:02d}", "工厂一", "品牌甲", "系列甲", f"商品{i}", "1"])
    with open(os.path.join(d, "sales.csv"), "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["period", "plant", "brand", "family", "sku", "units"])
        w.writerows(sales)

    # 生成 labor.csv（驱动第一步工厂→系列，规则 family 是"系列甲/系列乙"，覆盖 12 期）
    labor = [[f"2026-{p:02d}", "工厂一", "系列甲", "100"] for p in range(1, 13)]
    with open(os.path.join(d, "labor.csv"), "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(["period", "plant", "family", "hours"])
        w.writerows(labor)

    rule["name"] = "vault-big"
    # 第二步 target 的 sku INCLUDE 替换为放大后的商品列表
    for s in rule["stages"]:
        if "系列分到商品" in s["id"] or "第二步" in s["id"]:
            s["target"]["sku"]["include"] = sku_list
    rp = os.path.join(d, "rule.json")
    json.dump(rule, open(rp, "w", encoding="utf-8"), ensure_ascii=False)

    # 跑计算（gzip 证据）
    out = os.path.join(root, "out")
    t0 = time.time()
    r = run([EXE, "run", rp, out, "--workers", "4", "--evidence-gzip"])
    run_wall = time.time() - t0
    print(f"计算: rc={r.returncode} wall={run_wall:.2f}s")

    # vault add
    vault = os.path.join(root, "vault")
    t0 = time.time()
    r = run([EXE, "vault", "add", vault, "BIG", "v1", "official", out])
    add_wall = time.time() - t0
    add_ok = r.returncode == 0
    rec = json.loads(r.stdout[r.stdout.find("{"):r.stdout.rfind("}") + 1]) if add_ok else {}
    rid = rec.get("id", "")
    print(f"vault add: rc={r.returncode} wall={add_wall:.2f}s id={rid}")

    # stats
    r = run([EXE, "vault", "stats", vault])
    stats = json.loads(r.stdout[r.stdout.find("{"):r.stdout.rfind("}") + 1]) if r.returncode == 0 else {}
    print(f"vault stats: logical={stats.get('logical_bytes',0)/1024/1024:.1f}MB stored={stats.get('physical_bytes',0)/1024/1024:.1f}MB saved={stats.get('saved_bytes',0)/1024/1024:.1f}MB")

    # export-db
    bulk = os.path.join(root, "bulk.csv")
    t0 = time.time()
    r = run([EXE, "vault", "export-db", vault, rid, bulk])
    exp_wall = time.time() - t0
    exp_ok = r.returncode == 0
    rows = 0
    if exp_ok:
        with open(bulk, encoding="utf-8-sig", newline="") as f:
            rows = sum(1 for _ in csv.reader(f)) - 1
    print(f"export-db: rc={r.returncode} wall={exp_wall:.2f}s rows={rows} throughput={rows/max(exp_wall,1e-6):.0f} rows/s")

    # restore
    restored = os.path.join(root, "restored")
    t0 = time.time()
    r = run([EXE, "vault", "restore", vault, rid, restored])
    res_wall = time.time() - t0
    print(f"restore: rc={r.returncode} wall={res_wall:.2f}s")

    print(f"\n=== 汇总 ===")
    print(f"计算 {run_wall:.2f}s | vault add {add_wall:.2f}s | export-db {exp_wall:.2f}s ({rows}行) | restore {res_wall:.2f}s")

    # 清理
    import shutil
    shutil.rmtree(root, ignore_errors=True)


if __name__ == "__main__":
    import argparse
    ap = argparse.ArgumentParser()
    ap.add_argument("--skus", type=int, default=100000)
    args = ap.parse_args()
    main(args.skus)
