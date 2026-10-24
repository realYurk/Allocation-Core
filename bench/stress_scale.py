# -*- coding: utf-8 -*-
"""Allocation Core 可调规模压测生成器（仅合成数据，不依赖引擎源码）。

用法：
  python stress_scale.py OUT_DIR --accounts N --periods P --skus S
结果行数 ≈ accounts × periods × skus（每个池被分摊到所有 SKU）。

规模档位参考：
  百万级  --accounts 100 --periods 12 --skus 1000   => 1,200,000 行
  千万级  --accounts 100 --periods 12 --skus 10000  => 12,000,000 行
  亿级    --accounts 1000 --periods 12 --skus 10000 => 120,000,000 行
"""
import argparse
import csv
import json
import os


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('out', help='输出目录')
    ap.add_argument('--accounts', type=int, default=100)
    ap.add_argument('--periods', type=int, default=12)
    ap.add_argument('--skus', type=int, default=1000)
    ap.add_argument('--amount', default='5000.00')
    args = ap.parse_args()

    os.makedirs(args.out, exist_ok=True)
    dims = ['period', 'region', 'brand', 'account', 'sku', 'currency']

    # 金额表：每个池一条待分摊金额
    amounts = []
    for period in range(1, args.periods + 1):
        for acc in range(1, args.accounts + 1):
            amounts.append([
                '2026-%02d' % period, '东区', '品牌甲', '科目%03d' % acc,
                '待分摊', 'CNY', args.amount, 'SRC-%d-%d' % (period, acc)
            ])

    # 驱动表：每个 SKU 一条权重
    drivers = []
    for period in range(1, args.periods + 1):
        for sku in range(1, args.skus + 1):
            drivers.append([
                '2026-%02d' % period, '东区', '品牌甲', '商品%05d' % sku,
                str(1 + sku % 20)
            ])

    with open(os.path.join(args.out, 'amounts.csv'), 'w', newline='', encoding='utf-8') as f:
        w = csv.writer(f)
        w.writerow(dims + ['amount', 'row_id'])
        w.writerows(amounts)

    with open(os.path.join(args.out, 'drivers.csv'), 'w', newline='', encoding='utf-8') as f:
        w = csv.writer(f)
        w.writerow(['period', 'region', 'brand', 'sku', 'units'])
        w.writerows(drivers)

    dims_scope = {d: ('AS_IS' if d in ('period', 'region', 'brand', 'account', 'currency') else 'TOTAL') for d in dims}
    target = {d: {'mode': 'SOURCE'} for d in dims}
    target['sku'] = {'mode': 'INCLUDE', 'include': ['商品%05d' % s for s in range(1, args.skus + 1)]}

    spec = {
        'schema': 'allocation-ir/2.0',
        'name': 'scale benchmark %d accounts x %d periods x %d skus' % (args.accounts, args.periods, args.skus),
        'precision': 2,
        'dimensions': dims,
        'immutable_dimensions': ['period', 'currency'],
        'hierarchies': {},
        'datasets': {
            'amounts': {'expenses': 'amounts.csv'},
            'drivers': {'sales': 'drivers.csv'},
        },
        'stages': [{
            'id': 'allocate_by_sales',
            'inputs': [{'from': 'expenses', 'share': '1'}],
            'source': {'grouping': dims_scope, 'filters': {}},
            'target': target,
            'conservation_keys': ['period', 'region', 'brand', 'account', 'currency'],
            'allocations': [{
                'id': 'sales_share',
                'share': '1',
                'driver_dataset': 'sales',
                'weight_field': 'units',
                'negative_driver_policy': 'REJECT',
            }],
        }],
    }
    with open(os.path.join(args.out, 'rule.json'), 'w', encoding='utf-8') as f:
        json.dump(spec, f, ensure_ascii=False, indent=2)

    pools = args.accounts * args.periods
    expected = pools * args.skus
    print('pools=%d driver_rows=%d expected_output_rows=%d' % (pools, args.periods * args.skus, expected))


if __name__ == '__main__':
    main()
