"""Run a single stress scenario and collect timing / memory / correctness metrics.

Cross-platform-ish. On Windows, peak RSS is measured by polling the process via
a helper; on Linux/macOS, /usr/bin/time -v is preferred when available.

Usage:
  python run_stress.py --exe alloc_windows_amd64.exe --datadir DIR \
      --periods 12 --accounts 100 --skus 5000 --workers 4 \
      --disk-partitions 16 --driver-disk-index --out result.json
"""
import argparse
import json
import os
import shutil
import subprocess
import sys
import time

import gen_stress


def run_cmd(cmd):
    t0 = time.time()
    p = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    peak = 0
    try:
        import psutil  # optional
        proc = psutil.Process(p.pid)
        while p.poll() is None:
            try:
                rss = proc.memory_info().rss
                peak = max(peak, rss)
            except Exception:
                break
            time.sleep(0.05)
    except ImportError:
        pass
    out, err = p.communicate()
    wall = time.time() - t0
    return p.returncode, out, err, wall, peak


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--exe", default="alloc_windows_amd64.exe")
    ap.add_argument("--datadir", required=True)
    ap.add_argument("--periods", type=int, default=12)
    ap.add_argument("--accounts", type=int, default=100)
    ap.add_argument("--skus", type=int, default=5000)
    ap.add_argument("--multi-factor", action="store_true")
    ap.add_argument("--workers", type=int, default=4)
    ap.add_argument("--disk-partitions", type=int, default=0,
                    help="0 = memory executor, >=2 = disk executor")
    ap.add_argument("--driver-disk-index", action="store_true")
    ap.add_argument("--driver-cache-dir", default="")
    ap.add_argument("--evidence-gzip", action="store_true",
                    help="V18: stream gzip evidence instead of plaintext ledger")
    ap.add_argument("--label", default="")
    ap.add_argument("--out", default="result.json")
    ap.add_argument("--skip-gen", action="store_true")
    args = ap.parse_args()

    exe = os.path.abspath(args.exe)
    datadir = os.path.abspath(args.datadir)

    if not args.skip_gen:
        if os.path.exists(datadir):
            shutil.rmtree(datadir)
        argv = [datadir, "--periods", str(args.periods), "--accounts", str(args.accounts),
                "--skus", str(args.skus)]
        if args.multi_factor:
            argv.append("--multi-factor")
        gen_stress.main(argv)

    rule = os.path.join(datadir, "rule.json")
    outdir = os.path.join(datadir, "out")
    if os.path.exists(outdir):
        shutil.rmtree(outdir)

    cmd = [exe, "run", rule, outdir]
    if args.disk_partitions >= 2:
        cmd += ["--disk-partitions", str(args.disk_partitions)]
        if args.driver_disk_index:
            cmd += ["--driver-disk-index"]
            if args.driver_cache_dir:
                cmd += ["--driver-cache-dir", args.driver_cache_dir]
    if args.workers:
        cmd += ["--workers", str(args.workers)]
    if args.evidence_gzip:
        cmd += ["--evidence-gzip"]

    rc, out, err, wall, peak = run_cmd(cmd)

    result = {
        "label": args.label,
        "periods": args.periods,
        "accounts": args.accounts,
        "skus": args.skus,
        "multi_factor": args.multi_factor,
        "workers": args.workers,
        "disk_partitions": args.disk_partitions,
        "driver_disk_index": args.driver_disk_index,
        "evidence_gzip": args.evidence_gzip,
        "returncode": rc,
        "wall_seconds": round(wall, 3),
        "peak_rss_bytes": peak,
        "peak_rss_mb": round(peak / (1024 * 1024), 1),
        "status": "UNKNOWN",
    }
    if rc == 0:
        # parse summary JSON from stdout
        start = out.find("{")
        end = out.rfind("}")
        if start >= 0 and end >= 0:
            s = json.loads(out[start:end + 1])
            result["status"] = s.get("status")
            result["input_amount"] = s.get("input_amount")
            result["output_amount"] = s.get("output_amount")
            result["terminal_rows"] = s.get("terminal_rows")
            result["output_rows"] = s.get("output_rows")
            result["ledger_rows"] = s.get("ledger_rows")
            result["pool_checks"] = s.get("pool_checks")
            result["stages"] = s.get("stages")
    else:
        result["stderr"] = err[-2000:]

    # measure output file sizes
    if os.path.isdir(outdir):
        sizes = {}
        for fn in os.listdir(outdir):
            p = os.path.join(outdir, fn)
            if os.path.isfile(p):
                sizes[fn] = os.path.getsize(p)
        result["output_bytes"] = sizes

    with open(args.out, "w", encoding="utf-8") as f:
        json.dump(result, f, indent=2, ensure_ascii=False)
    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    main()
