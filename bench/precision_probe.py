# -*- coding: utf-8 -*-
"""Allocation Core 精度与守恒边界验证（纯二进制级，不依赖 Go 工具链）。

验证项：
  1. 最大余数法：3 路权重 1/1/1 分摊 100 分 => 34/33/33，余数给最大者（确定性）
  2. 精度 0（整数元）
  3. 精度 6（微元，unit=1e6）
  4. 大额接近 int64 上限（9 万亿 + 小数）
  5. 有符号负权重（negative_driver_policy）
  6. 跨池守恒（多科目分摊到多 SKU，每池 input==output）
  7. 结果行确定性（同输入两次运行 SHA 一致）
"""
import csv
import hashlib
import json
import os
import subprocess
import sys
from decimal import Decimal
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
EXE = ROOT / 'core' / 'alloc_windows_amd64.exe'


def sha(p):
    h = hashlib.sha256()
    with open(p, 'rb') as f:
        for b in iter(lambda: f.read(1024 * 1024), b''):
            h.update(b)
    return h.hexdigest()


def exe(args, timeout=120):
    p = subprocess.run([str(EXE)] + [str(x) for x in args], capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=timeout)
    if p.returncode != 0:
        raise AssertionError('exit %d: %s\nstderr:%s' % (p.returncode, args, p.stderr[-1500:]))
    return p


def write_case(base, precision, amounts, drivers):
    os.makedirs(base, exist_ok=True)
    dims = ['period', 'region', 'brand', 'account', 'sku', 'currency']
    with open(base / 'amounts.csv', 'w', newline='', encoding='utf-8') as f:
        w = csv.writer(f)
        w.writerow(dims + ['amount', 'row_id'])
        w.writerows(amounts)
    with open(base / 'drivers.csv', 'w', newline='', encoding='utf-8') as f:
        w = csv.writer(f)
        w.writerow(['period', 'region', 'brand', 'sku', 'units'])
        w.writerows(drivers)
    skus = sorted({r[3] for r in drivers})
    spec = {
        'schema': 'allocation-ir/2.0', 'name': 'precision probe', 'precision': precision,
        'dimensions': dims, 'immutable_dimensions': ['period', 'currency'], 'hierarchies': {},
        'datasets': {'amounts': {'a': 'amounts.csv'}, 'drivers': {'d': 'drivers.csv'}},
        'stages': one_stage(skus),
    }
    with open(base / 'rule.json', 'w', encoding='utf-8') as f:
        json.dump(spec, f, ensure_ascii=False, indent=2)


def one_stage(skus):
    target = {d: {'mode': 'SOURCE'} for d in ['period', 'region', 'brand', 'account', 'currency']}
    target['sku'] = {'mode': 'INCLUDE', 'include': skus}
    return [{
        'id': 's', 'inputs': [{'from': 'a', 'share': '1'}],
        'source': {'grouping': {'period': 'AS_IS', 'region': 'AS_IS', 'brand': 'AS_IS', 'account': 'AS_IS', 'sku': 'TOTAL', 'currency': 'AS_IS'}, 'filters': {}},
        'target': target,
        'conservation_keys': ['period', 'region', 'brand', 'account', 'currency'],
        'allocations': [{'id': 'x', 'share': '1', 'driver_dataset': 'd', 'weight_field': 'units', 'negative_driver_policy': 'REJECT'}],
    }]


def run_and_check(base, out):
    if out.exists():
        import shutil; shutil.rmtree(out)
    exe(['run', base / 'rule.json', out])
    s = json.loads((out / 'run_summary.json').read_text(encoding='utf-8-sig'))
    assert s['status'] == 'PASS', 'status ' + str(s)
    inp = Decimal(s['input_amount']); oup = Decimal(s['output_amount'])
    assert inp == oup, 'non-conservation %s vs %s' % (inp, oup)
    return s, sha(out / 'results.csv')


def main():
    results = []

    # 1. 最大余数法：100 元分给 3 个权重相等的 SKU
    base = ROOT / 'bench' / 'precision' / 'remainder'
    write_case(base, 2,
               [['2026-01', '东区', '品牌甲', '科目001', '待分摊', 'CNY', '1.00', 'S1']],
               [['2026-01', '东区', '品牌甲', '商品A', '1'], ['2026-01', '东区', '品牌甲', '商品B', '1'], ['2026-01', '东区', '品牌甲', '商品C', '1']])
    s, h = run_and_check(base, ROOT / 'bench' / 'precision' / 'remainder_out')
    rows = {}
    with open(ROOT / 'bench' / 'precision' / 'remainder_out' / 'results.csv', encoding='utf-8-sig') as f:
        for r in csv.DictReader(f):
            rows[r['sku']] = r['amount']
    # 100 分 => 34/33/33
    vals = sorted(rows.values())
    assert vals == ['0.33', '0.33', '0.34'], 'remainder wrong: %s' % vals
    assert Decimal(rows['商品A']) == Decimal('0.34'), 'largest remainder not to first identity'
    results.append({'case': 'largest_remainder_1_cent_over_3', 'check': '0.34/0.33/0.33', 'status': 'PASS'})

    # 2. 精度 0
    base = ROOT / 'bench' / 'precision' / 'p0'
    write_case(base, 0,
               [['2026-01', '东区', '品牌甲', '科目001', '待分摊', 'CNY', '100', 'S1']],
               [['2026-01', '东区', '品牌甲', '商品A', '1'], ['2026-01', '东区', '品牌甲', '商品B', '2']])
    s, h = run_and_check(base, ROOT / 'bench' / 'precision' / 'p0_out')
    results.append({'case': 'precision_0_integer', 'status': 'PASS', 'total': s['input_amount']})

    # 3. 精度 6
    base = ROOT / 'bench' / 'precision' / 'p6'
    write_case(base, 6,
               [['2026-01', '东区', '品牌甲', '科目001', '待分摊', 'CNY', '123.456789', 'S1']],
               [['2026-01', '东区', '品牌甲', '商品A', '1'], ['2026-01', '东区', '品牌甲', '商品B', '3']])
    s, h = run_and_check(base, ROOT / 'bench' / 'precision' / 'p6_out')
    results.append({'case': 'precision_6_micro', 'status': 'PASS', 'total': s['input_amount']})

    # 4. 大额 9 万亿（接近 int64 上限）
    base = ROOT / 'bench' / 'precision' / 'big'
    write_case(base, 2,
               [['2026-01', '东区', '品牌甲', '科目001', '待分摊', 'CNY', '9000000000000.99', 'S1']],
               [['2026-01', '东区', '品牌甲', '商品A', '7'], ['2026-01', '东区', '品牌甲', '商品B', '3']])
    s, h = run_and_check(base, ROOT / 'bench' / 'precision' / 'big_out')
    results.append({'case': 'large_9_trillion', 'status': 'PASS', 'total': s['input_amount']})

    # 5. 负权重 REJECT（应失败）
    base = ROOT / 'bench' / 'precision' / 'neg'
    write_case(base, 2,
               [['2026-01', '东区', '品牌甲', '科目001', '待分摊', 'CNY', '100.00', 'S1']],
               [['2026-01', '东区', '品牌甲', '商品A', '1'], ['2026-01', '东区', '品牌甲', '商品B', '-1']])
    p = subprocess.run([str(EXE), 'run', str(base / 'rule.json'), str(ROOT / 'bench' / 'precision' / 'neg_out')], capture_output=True, text=True, encoding='utf-8', errors='replace')
    assert p.returncode != 0, 'negative driver not rejected'
    results.append({'case': 'negative_driver_rejected', 'status': 'PASS', 'exit': p.returncode})

    # 6. 跨池守恒：3 科目 × 4 SKU，每池独立守恒
    base = ROOT / 'bench' / 'precision' / 'multi_pool'
    amts = [['2026-01', '东区', '品牌甲', '科目%03d' % i, '待分摊', 'CNY', '%.2f' % (i * 111.11), 'S%d' % i] for i in range(1, 4)]
    drv = [['2026-01', '东区', '品牌甲', '商品%c' % chr(64 + j), str(j)] for j in range(1, 5)]
    write_case(base, 2, amts, drv)
    s, h = run_and_check(base, ROOT / 'bench' / 'precision' / 'multi_pool_out')
    # 每池检查 reconciliation.csv
    npools = 0
    with open(ROOT / 'bench' / 'precision' / 'multi_pool_out' / 'reconciliation.csv', encoding='utf-8-sig') as f:
        for r in csv.DictReader(f):
            assert r['status'] == 'PASS' and Decimal(r['delta']) == 0
            npools += 1
    results.append({'case': 'multi_pool_conservation', 'pools': npools, 'status': 'PASS'})

    # 7. 确定性：同输入两次运行 results.csv SHA 一致
    base = ROOT / 'bench' / 'precision' / 'det'
    write_case(base, 2,
               [['2026-01', '东区', '品牌甲', '科目001', '待分摊', 'CNY', '123.45', 'S1']],
               [['2026-01', '东区', '品牌甲', '商品A', '1'], ['2026-01', '东区', '品牌甲', '商品B', '2'], ['2026-01', '东区', '品牌甲', '商品C', '3']])
    s1, h1 = run_and_check(base, ROOT / 'bench' / 'precision' / 'det_out1')
    s2, h2 = run_and_check(base, ROOT / 'bench' / 'precision' / 'det_out2')
    assert h1 == h2, 'nondeterministic output'
    results.append({'case': 'determinism_sha256_equal', 'status': 'PASS'})

    print(json.dumps(results, ensure_ascii=False, indent=2))
    (ROOT / 'bench' / 'precision' / 'precision_results.json').write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding='utf-8')


if __name__ == '__main__':
    main()
