# -*- coding: utf-8 -*-
"""Allocation Core 压测运行器：计时 + 守恒校验 + 内存采样。

用法：
  python bench/stress_run.py --case bench/scale_1m --out bench/runs/1m --workers 4
"""
import argparse
import csv
import json
import os
import subprocess
import sys
import time
from decimal import Decimal
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
EXE = ROOT / 'core' / 'alloc_windows_amd64.exe'


def run(cmd, timeout=3600):
    t0 = time.perf_counter()
    p = subprocess.run([str(x) for x in cmd], capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=timeout)
    dt = time.perf_counter() - t0
    if p.returncode != 0:
        raise RuntimeError('nonzero exit %d: %s\nstdout:%s\nstderr:%s' % (p.returncode, cmd, p.stdout[-1500:], p.stderr[-1500:]))
    return dt, p.stdout


def verify(out):
    with open(out / 'run_summary.json', encoding='utf-8-sig') as f:
        s = json.load(f)
    assert s['status'] == 'PASS', 'status not PASS'
    inp = Decimal(s['input_amount'])
    oup = Decimal(s['output_amount'])
    assert inp == oup, 'non-conservation input=%s output=%s' % (inp, oup)

    pools = 0
    with open(out / 'reconciliation.csv', encoding='utf-8-sig') as f:
        for r in csv.DictReader(f):
            assert r['status'] == 'PASS' and Decimal(r['delta']) == 0, 'pool mismatch ' + str(r)
            pools += 1

    rows = 0
    total = Decimal(0)
    with open(out / 'results.csv', encoding='utf-8-sig') as f:
        for r in csv.DictReader(f):
            total += Decimal(r['amount'])
            rows += 1
    assert rows == int(s['terminal_rows']), 'terminal rows mismatch'
    assert total == inp, 'result sum mismatch'
    return rows, pools, str(total)


def dir_bytes(p):
    return sum(x.stat().st_size for x in p.rglob('*') if x.is_file())


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--case', required=True)
    ap.add_argument('--out', required=True)
    ap.add_argument('--workers', type=int, default=4)
    ap.add_argument('--disk-partitions', type=int, default=0)
    args = ap.parse_args()

    rule = Path(args.case) / 'rule.json'
    out = Path(args.out)
    # 引擎要求输出目录不存在，由其自身创建，故此处不预先 mkdir。
    if out.exists():
        import shutil
        shutil.rmtree(out)

    cmd = [str(EXE), 'run', str(rule), str(out), '--workers', str(args.workers), '--evidence-gzip']
    if args.disk_partitions >= 2:
        cmd += ['--disk-partitions', str(args.disk_partitions)]

    dt, stdout = run(cmd)
    rows, pools, total = verify(out)
    summ = json.loads((out / 'run_summary.json').read_text(encoding='utf-8-sig'))
    result = {
        'case': Path(args.case).name,
        'seconds': round(dt, 3),
        'rows': rows,
        'pools': pools,
        'total': total,
        'output_bytes': dir_bytes(out),
        'workers': args.workers,
        'disk_partitions': args.disk_partitions,
        'input_amount': summ.get('input_amount'),
        'output_amount': summ.get('output_amount'),
        'conserved': summ.get('input_amount') == summ.get('output_amount'),
    }
    print(json.dumps(result, ensure_ascii=False, indent=2))
    (Path(args.out) / 'stress_result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding='utf-8')


if __name__ == '__main__':
    main()
