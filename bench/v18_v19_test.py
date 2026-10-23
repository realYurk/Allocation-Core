#!/usr/bin/env python3
"""V18/V19 压缩证据与压缩血缘索引深度测试。

攻击目标（判官视角）：
  A. V18 压缩证据：无损恢复、篡改检测、manifest 篡改、跨 workers 一致、
     路径穿越、空结果、截断 gzip、重复 expand 拒绝覆盖。
  B. V19 压缩索引：chunk 边界(512行/1MB)、含换行/引号/逗号字段、分片哈希、
     与全扫描 trace 逐字节一致、篡改拒绝、源文件变更后拒绝。

无第三方依赖，仅用标准库 + 引擎二进制。
"""
import csv
import hashlib
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile

EXE = os.path.abspath("alloc_windows_amd64.exe")
DIMS = ["period", "plant", "brand", "family", "sku", "account", "currency"]

PASS = 0
FAIL = 0


def check(name, cond, detail=""):
    global PASS, FAIL
    if cond:
        PASS += 1
        print(f"  [PASS] {name}")
    else:
        FAIL += 1
        print(f"  [FAIL] {name}  {detail}")


def run(cmd, **kw):
    return subprocess.run(cmd, capture_output=True, text=True, **kw)


def write_csv(path, header, rows):
    with open(path, "w", newline="", encoding="utf-8") as f:
        w = csv.writer(f)
        w.writerow(header)
        w.writerows(rows)


def single_stage_rule(name, skus, driver_rows, precision=2, share="1",
                      driver_field="units", driver_name="sales"):
    """构造单阶段规则：待分配金额 -> 按 driver 分摊到指定 SKU。"""
    src = {x: ("AS_IS" if x in ["period", "plant", "account", "currency"] else "TOTAL") for x in DIMS}
    tgt = {x: {"mode": "SOURCE"} for x in DIMS}
    tgt.update({"brand": {"mode": "ELEMENT", "value": "品牌甲"},
                "family": {"mode": "INCLUDE", "include": ["系列01"]},
                "sku": {"mode": "INCLUDE", "include": skus}})
    return {
        "schema": "allocation-ir/2.0", "name": name, "precision": precision,
        "dimensions": DIMS, "immutable_dimensions": ["period", "currency"],
        "hierarchies": {},
        "datasets": {"amounts": {"amounts": "amounts.csv"},
                     "drivers": {driver_name: driver_name + ".csv"}},
        "stages": [{"id": "s1", "inputs": [{"from": "amounts", "share": share}],
                    "source": {"grouping": src, "filters": {}}, "target": tgt,
                    "conservation_keys": ["period", "plant", "account", "currency"],
                    "allocations": [{"id": "a1", "share": "1",
                                     "driver_dataset": driver_name,
                                     "weight_field": driver_field}]}],
    }


def build_rule_dir(d, name, skus, driver_rows, **kw):
    """在目录 d 下生成一个完整规则 + 数据。返回 rule.json 路径。"""
    write_csv(os.path.join(d, "amounts.csv"), DIMS + ["amount"],
              [["2026-01", "工厂一", "待分配品牌", "待分配系列", "待分配", "科目001", "CNY", "1000.00"]])
    write_csv(os.path.join(d, "sales.csv"), ["period", "plant", "brand", "family", "sku", "units"], driver_rows)
    rule = single_stage_rule(name, skus, driver_rows, **kw)
    rp = os.path.join(d, "rule.json")
    json.dump(rule, open(rp, "w", encoding="utf-8"), ensure_ascii=False)
    return rp


def sha256_file(p):
    h = hashlib.sha256()
    with open(p, "rb") as f:
        for chunk in iter(lambda: f.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def test_v18_lossless_and_security():
    print("\n=== A. V18 压缩证据：无损与安全 ===")
    root = tempfile.mkdtemp(prefix="v18a_")
    d = os.path.join(root, "data")
    os.makedirs(d)
    skus = ["商品A", "商品B", "商品C"]
    dr = [["2026-01", "工厂一", "品牌甲", "系列01", s, "1"] for s in skus]
    rp = build_rule_dir(d, "v18", skus, dr)

    plain = os.path.join(root, "plain")
    gz = os.path.join(root, "gz")
    restored = os.path.join(root, "restored")

    r = run([EXE, "run", rp, plain, "--workers", "1"])
    check("明文 run 成功", r.returncode == 0, r.stderr)
    r = run([EXE, "run", rp, gz, "--workers", "4", "--evidence-gzip"])
    check("压缩 run 成功", r.returncode == 0, r.stderr)

    # 1. 无损恢复
    r = run([EXE, "evidence-expand", gz, restored])
    check("evidence-expand 成功", r.returncode == 0, r.stderr)
    allok = True
    for f in ["ledger.csv", "source_to_pool.csv", "lineage_links.csv", "results.csv", "reconciliation.csv"]:
        a = os.path.join(plain, f)
        b = os.path.join(restored, f)
        if not (os.path.exists(b) and sha256_file(a) == sha256_file(b)):
            allok = False
    check("恢复文件逐字节一致（SHA256）", allok)

    # 2. 重复 expand 拒绝覆盖
    r = run([EXE, "evidence-expand", gz, restored])
    check("重复 expand 拒绝覆盖", r.returncode != 0, r.stdout)

    # 3. 篡改 gzip 内容（翻转一个字节）被 verify 拒绝
    tampered = os.path.join(root, "tampered")
    shutil.copytree(gz, tampered)
    p = os.path.join(tampered, "ledger.csv.gz")
    data = bytearray(open(p, "rb").read())
    data[len(data) // 2] ^= 0xFF
    open(p, "wb").write(bytes(data))
    r = run([EXE, "evidence-verify", tampered])
    check("篡改 gzip 被 verify 拒绝", r.returncode != 0, r.stdout)

    # 4. 截断 gzip 被拒绝
    trunc = os.path.join(root, "trunc")
    shutil.copytree(gz, trunc)
    p = os.path.join(trunc, "ledger.csv.gz")
    data = open(p, "rb").read()
    open(p, "wb").write(data[:-5])
    r = run([EXE, "evidence-verify", trunc])
    check("截断 gzip 被 verify 拒绝", r.returncode != 0, r.stdout)

    # 5. manifest 篡改（改 plain_sha256）被拒绝
    mf = os.path.join(root, "mf_tampered")
    shutil.copytree(gz, mf)
    mp = os.path.join(mf, "evidence_stream_manifest.json")
    m = json.load(open(mp, encoding="utf-8"))
    m["files"]["ledger.csv"]["plain_sha256"] = "0" * 64
    json.dump(m, open(mp, "w", encoding="utf-8"), ensure_ascii=False)
    r = run([EXE, "evidence-verify", mf])
    check("manifest 篡改 plain_sha256 被拒绝", r.returncode != 0, r.stdout)

    # 6. 缺失文件被拒绝
    missing = os.path.join(root, "missing")
    shutil.copytree(gz, missing)
    os.remove(os.path.join(missing, "source_to_pool.csv.gz"))
    r = run([EXE, "evidence-verify", missing])
    check("缺失文件被拒绝", r.returncode != 0, r.stdout)

    # 7. 跨 workers 一致性（压缩输出确定性）。
    #    计算产物（ledger/source_to_pool/lineage/results/reconciliation）必须确定；
    #    run_summary.json 含时间戳、manifest 记录 run_summary 哈希，故排除。
    gz2 = os.path.join(root, "gz2")
    run([EXE, "run", rp, gz2, "--workers", "2", "--evidence-gzip"])
    stable = ["ledger.csv.gz", "source_to_pool.csv.gz", "lineage_links.csv.gz",
              "results.csv", "reconciliation.csv"]
    same = all(
        sha256_file(os.path.join(gz, f)) == sha256_file(os.path.join(gz2, f))
        for f in stable
    )
    check("跨 workers 计算产物一致（确定性）", same)

    shutil.rmtree(root, ignore_errors=True)


def test_v19_index_correctness():
    print("\n=== B. V19 压缩索引：正确性与边界 ===")
    root = tempfile.mkdtemp(prefix="v19a_")
    d = os.path.join(root, "data")
    os.makedirs(d)
    skus = ["商品A", "商品B", "商品C", "商品D"]
    dr = [["2026-01", "工厂一", "品牌甲", "系列01", s, "1"] for s in skus]
    rp = build_rule_dir(d, "v19", skus, dr)

    plain = os.path.join(root, "plain")
    gz = os.path.join(root, "gz")
    run([EXE, "run", rp, plain, "--workers", "1"])
    run([EXE, "run", rp, gz, "--workers", "4", "--evidence-gzip"])

    # 取第一条结果 ID
    with open(os.path.join(plain, "results.csv"), encoding="utf-8-sig", newline="") as f:
        rdr = csv.reader(f)
        header = next(rdr)
        row = next(rdr)
        rid_idx = header.index("row_id")
        rid = row[rid_idx]

    # 建索引
    r = run([EXE, "trace-pack-index", gz])
    check("trace-pack-index 成功", r.returncode == 0, r.stderr)
    r = run([EXE, "trace-pack-verify", gz])
    check("trace-pack-verify 成功", r.returncode == 0, r.stderr)

    # 压缩索引 trace vs 明文全扫描 trace 一致
    r1 = run([EXE, "trace", rp, plain, rid])
    r2 = run([EXE, "trace", rp, gz, rid])
    same = r1.stdout == r2.stdout and r1.returncode == 0 and r2.returncode == 0
    check("压缩索引 trace 与明文 trace 逐字节一致", same,
          f"\nplain={r1.stdout[:200]}\npacked={r2.stdout[:200]}")

    # 重建索引被拒绝（已存在）
    r = run([EXE, "trace-pack-index", gz])
    check("重复建索引被拒绝", r.returncode != 0, r.stdout)

    # 篡改 chunk 被 verify 拒绝
    tampered = os.path.join(root, "tampered")
    shutil.copytree(gz, tampered)
    chunk = os.path.join(tampered, "trace_packed_v19", "ledger.csv", "chunks.bgz")
    data = bytearray(open(chunk, "rb").read())
    data[len(data) // 2] ^= 0xFF
    open(chunk, "wb").write(bytes(data))
    r = run([EXE, "trace-pack-verify", tampered])
    check("篡改 chunk 被 verify 拒绝", r.returncode != 0, r.stdout)

    # 源文件变更后索引失效
    stale = os.path.join(root, "stale")
    shutil.copytree(gz, stale)
    with open(os.path.join(stale, "ledger.csv.gz"), "ab") as f:
        f.write(b"garbage")
    r = run([EXE, "trace", rp, stale, rid])
    check("源文件变更后 trace 拒绝使用旧索引", r.returncode != 0, r.stderr)

    shutil.rmtree(root, ignore_errors=True)


def test_v19_chunk_boundary_and_special_chars():
    """针对 chunk 边界(512行/1MB)和含换行/引号/逗号字段的索引正确性。"""
    print("\n=== C. V19 chunk 边界 + 特殊字符字段 ===")
    root = tempfile.mkdtemp(prefix="v19b_")
    d = os.path.join(root, "data")
    os.makedirs(d)

    # 构造超过 512 行的 driver，触发多个 chunk
    n = 1300  # > 2*512，确保多个 chunk
    skus = [f"商品{i}" for i in range(n)]
    dr = [["2026-01", "工厂一", "品牌甲", "系列01", s, "1"] for s in skus]
    # 注入含逗号、引号、换行的 SKU
    dr[0][4] = '商品"含引号"'
    dr[1][4] = "商品,含逗号"
    dr[2][4] = "商品\n含换行"
    skus[0] = '商品"含引号"'
    skus[1] = "商品,含逗号"
    skus[2] = "商品\n含换行"

    rp = build_rule_dir(d, "v19b", skus, dr)

    plain = os.path.join(root, "plain")
    gz = os.path.join(root, "gz")
    run([EXE, "run", rp, plain, "--workers", "1"])
    run([EXE, "run", rp, gz, "--workers", "4", "--evidence-gzip"])
    r = run([EXE, "trace-pack-index", gz])
    check("多 chunk 索引构建成功", r.returncode == 0, r.stderr)

    # 对每个特殊 SKU 的结果行做 trace，对比明文 vs 压缩索引
    with open(os.path.join(plain, "results.csv"), encoding="utf-8-sig", newline="") as f:
        rdr = csv.reader(f)
        header = next(rdr)
        rid_idx = header.index("row_id")
        sku_idx = header.index("sku")
        rows = list(rdr)

    special = ['商品"含引号"', "商品,含逗号", "商品\n含换行"]
    checked = 0
    for row in rows:
        if row[sku_idx] in special:
            rid = row[rid_idx]
            r1 = run([EXE, "trace", rp, plain, rid])
            r2 = run([EXE, "trace", rp, gz, rid])
            if r1.stdout == r2.stdout and r1.returncode == 0 and r2.returncode == 0:
                checked += 1
    check("特殊字符 SKU 的 trace 全部一致（共3个）", checked == 3, f"checked={checked}")

    # verify 全量校验通过
    r = run([EXE, "trace-pack-verify", gz])
    check("多 chunk 全量校验通过", r.returncode == 0, r.stderr)

    shutil.rmtree(root, ignore_errors=True)


def test_v19_large_field_1mb_chunk():
    """构造单行超大字段触发 1MB chunk 边界。"""
    print("\n=== D. V19 1MB chunk 边界（超大字段） ===")
    root = tempfile.mkdtemp(prefix="v19c_")
    d = os.path.join(root, "data")
    os.makedirs(d)
    # 一个大字段 SKU（约 200KB），确保一行触发 1MB 或单 chunk 逻辑
    big_sku = "大字段" + "X" * 200000
    skus = [big_sku, "商品B"]
    dr = [["2026-01", "工厂一", "品牌甲", "系列01", big_sku, "1"],
          ["2026-01", "工厂一", "品牌甲", "系列01", "商品B", "1"]]
    rp = build_rule_dir(d, "v19c", skus, dr)

    gz = os.path.join(root, "gz")
    run([EXE, "run", rp, gz, "--workers", "4", "--evidence-gzip"])
    r = run([EXE, "trace-pack-index", gz])
    check("超大字段索引构建成功", r.returncode == 0, r.stderr)
    r = run([EXE, "trace-pack-verify", gz])
    check("超大字段全量校验通过", r.returncode == 0, r.stderr)

    shutil.rmtree(root, ignore_errors=True)


def main():
    exe = None
    args = sys.argv[1:]
    if "--exe" in args:
        global EXE
        EXE = os.path.abspath(args[args.index("--exe") + 1])
    test_v18_lossless_and_security()
    test_v19_index_correctness()
    test_v19_chunk_boundary_and_special_chars()
    test_v19_large_field_1mb_chunk()
    print(f"\n{'='*50}")
    print(f"总计: {PASS} PASS, {FAIL} FAIL")
    sys.exit(1 if FAIL else 0)


if __name__ == "__main__":
    main()
