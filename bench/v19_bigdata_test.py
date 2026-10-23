#!/usr/bin/env python3
"""V19 压缩血缘索引在大数据下的性能与导出测试。

在既有 gzip 运行目录上：
  1. 统计压缩证据体积（明文等效 vs 实际存储）
  2. trace-pack-index 构建耗时/内存
  3. trace-pack-verify 全量校验耗时/内存
  4. 压缩索引 trace 查询 vs 无索引全扫描查询的耗时对比
  5. 索引目录体积（额外存储代价）
"""
import argparse
import json
import os
import subprocess
import time


def run_cmd(cmd):
    t0 = time.time()
    p = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    peak = 0
    try:
        import psutil
        proc = psutil.Process(p.pid)
        while p.poll() is None:
            try:
                peak = max(peak, proc.memory_info().rss)
            except Exception:
                break
            time.sleep(0.05)
    except ImportError:
        pass
    out, err = p.communicate()
    return p.returncode, out, err, time.time() - t0, peak


def dir_size(path):
    total = 0
    for root, _, files in os.walk(path):
        for f in files:
            total += os.path.getsize(os.path.join(root, f))
    return total


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--exe", default="alloc_windows_amd64.exe")
    ap.add_argument("--rundir", required=True, help="gzip 运行目录")
    ap.add_argument("--rule", required=True, help="rule.json 路径")
    ap.add_argument("--out", default="v19_result.json")
    args = ap.parse_args()

    exe = os.path.abspath(args.exe)
    rundir = os.path.abspath(args.rundir)
    rule = os.path.abspath(args.rule)

    res = {"rundir": rundir}

    # 1. 证据体积
    sizes = {}
    for f in os.listdir(rundir):
        p = os.path.join(rundir, f)
        if os.path.isfile(p):
            sizes[f] = os.path.getsize(p)
    res["evidence_files"] = sizes

    # 2. 取第一条结果 row_id
    import csv
    rid = None
    with open(os.path.join(rundir, "results.csv"), encoding="utf-8-sig", newline="") as f:
        rdr = csv.reader(f)
        header = next(rdr)
        row = next(rdr)
        rid = row[header.index("row_id")]
    res["row_id"] = rid

    # 3. 无索引全扫描查询（先删掉可能存在的索引）
    idx = os.path.join(rundir, "trace_packed_v19")
    if os.path.exists(idx):
        import shutil
        shutil.rmtree(idx)

    rc, out, err, wall, peak = run_cmd([exe, "trace", rule, rundir, rid])
    res["trace_noindex"] = {"rc": rc, "wall": round(wall, 4), "peak_mb": round(peak / 1048576, 1)}

    # 4. 建索引
    rc, out, err, wall, peak = run_cmd([exe, "trace-pack-index", rundir])
    res["index_build"] = {"rc": rc, "wall": round(wall, 4), "peak_mb": round(peak / 1048576, 1),
                          "stderr": err[-500:] if rc else ""}

    if rc == 0:
        res["index_size_bytes"] = dir_size(idx)

        # 5. 全量校验
        rc, out, err, wall, peak = run_cmd([exe, "trace-pack-verify", rundir])
        res["index_verify"] = {"rc": rc, "wall": round(wall, 4), "peak_mb": round(peak / 1048576, 1)}

        # 6. 有索引查询
        rc, out, err, wall, peak = run_cmd([exe, "trace", rule, rundir, rid])
        res["trace_indexed"] = {"rc": rc, "wall": round(wall, 4), "peak_mb": round(peak / 1048576, 1)}

        if res["trace_noindex"]["rc"] == 0 and rc == 0:
            speedup = res["trace_noindex"]["wall"] / max(res["trace_indexed"]["wall"], 1e-6)
            res["trace_speedup"] = round(speedup, 1)

    with open(args.out, "w", encoding="utf-8") as f:
        json.dump(res, f, indent=2, ensure_ascii=False)
    print(json.dumps(res, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
