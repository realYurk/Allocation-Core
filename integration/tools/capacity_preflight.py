#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""V21.1 integration-layer preflight. Core is never modified.

Estimate is deliberately conservative, NOT an exact prediction of engine output.
For an accurate plan, developers must supply the number of actual conserved
pools and effective target records per pool based on their business mapping.
"""
import argparse
import json
import shutil
import subprocess
import sys
from decimal import Decimal, InvalidOperation
from pathlib import Path

GIB = 1024 ** 3


def positive_decimal(value):
    try:
        number = Decimal(str(value))
    except InvalidOperation as exc:
        raise argparse.ArgumentTypeError('not a number: '+str(value)) from exc
    if not number.is_finite() or number <= 0:
        raise argparse.ArgumentTypeError('requires a positive finite number')
    return number


def positive_int(value):
    try:
        number = int(value)
    except ValueError as exc:
        raise argparse.ArgumentTypeError('requires an integer') from exc
    if number < 1 or number > 10 ** 12:
        raise argparse.ArgumentTypeError('must be within 1..10^12')
    return number


def parser():
    p = argparse.ArgumentParser(description='Pre-run disk/row budget guard (NO financial calculation).')
    p.add_argument('--pools', type=positive_int, required=True,
                   help='Conserved source pool count for the largest terminal stage')
    p.add_argument('--targets-per-pool', type=positive_int, required=True,
                   help='Effective targets per pool (not total global SKU catalogue)')
    p.add_argument('--factor-paths', type=positive_int, default=1,
                   help='Estimated output-producing allocation branches / factors; no automatic dedup')
    p.add_argument('--bytes-per-result', type=positive_int, default=144,
                   help='Measured/assumed results.csv bytes per output row; default 144')
    p.add_argument('--bytes-per-ledger', type=positive_int, default=300,
                   help='Measured/assumed uncompressed ledger bytes per row; default 300')
    p.add_argument('--overhead-factor', type=positive_decimal, default=Decimal('1.5'),
                   help='Extra disk for other evidence, snapshots and stage spool; default 1.5')
    p.add_argument('--max-rows', type=positive_int, default=50000000,
                   help='Hard budget ceiling in terminal rows; default 50 million')
    p.add_argument('--max-output-gib', type=positive_decimal, default=Decimal('100'),
                   help='Hard budget ceiling for estimated temporary storage; default 100 GiB')
    p.add_argument('--reserve-free-gib', type=positive_decimal, default=Decimal('5'),
                   help='Disk space to reserve after run; default 5 GiB')
    p.add_argument('--disk-path', type=Path, default=Path('.'),
                   help='Planned filesystem; nonexistent path uses nearest existing parent')
    p.add_argument('--json-report', type=Path, default=None)
    p.add_argument('--exec', dest='command', nargs=argparse.REMAINDER,
                   help='Optional frozen engine command; launch ONLY if budget passes')
    return p


def plan(args, free_bytes):
    estimated_rows = args.pools * args.targets_per_pool * args.factor_paths
    # No assumed gzip savings: plan for worst-case ledger as plaintext.
    base_bytes = estimated_rows * (args.bytes_per_result + args.bytes_per_ledger)
    reserve_bytes = int((Decimal(base_bytes) * args.overhead_factor).to_integral_value(rounding='ROUND_CEILING'))
    max_bytes = int(args.max_output_gib * GIB)
    free_after_reserve = free_bytes - int(args.reserve_free_gib * GIB)
    reasons = []
    if estimated_rows > args.max_rows:
        reasons.append('estimated terminal rows exceed --max-rows')
    if reserve_bytes > max_bytes:
        reasons.append('estimated temporary bytes exceed --max-output-gib')
    if reserve_bytes > max(0, free_after_reserve):
        reasons.append('insufficient free disk after mandatory free-space reserve')
    return {
        'status': 'BLOCK' if reasons else 'PASS',
        'input': {'pools': args.pools, 'targets_per_pool': args.targets_per_pool,
                  'factor_paths': args.factor_paths},
        'estimated_terminal_rows': estimated_rows,
        'estimated_uncompressed_result_and_ledger_bytes': base_bytes,
        'estimated_peak_storage_bytes': reserve_bytes,
        'estimated_peak_storage_gib': str((Decimal(reserve_bytes) / GIB).quantize(Decimal('0.01'))),
        'current_disk_free_bytes': free_bytes,
        'reasons': reasons,
        'method': 'planning envelope = pools * effective targets * branches * '
                  '(bytes per result + bytes per uncompressed ledger) * overhead factor',
        'critical_limitations': [
            'Not an exact row count or a strict runtime quota; data skew and DAG merges can change results.',
            'Amount, intermediate stages, driver indexes, Vault and DB imports may need additional disk.',
            'This wrapper must be on every production launch path; direct core CLI/HTTP calls bypass it.',
            'For hard safety deploy OS disk quota / separate filesystem and an external task supervisor.',
        ]
    }


def existing_parent(path):
    path = path.resolve()
    while not path.exists():
        if path.parent == path:
            raise ValueError('No existing ancestor for '+str(path))
        path = path.parent
    if not path.is_dir():
        path = path.parent
    return path


def main(argv=None):
    args = parser().parse_args(argv)
    if args.overhead_factor < 1:
        print('ERROR: overhead factor must be >=1', file=sys.stderr)
        return 2
    free = shutil.disk_usage(existing_parent(args.disk_path)).free
    result = plan(args, free)
    serialized = json.dumps(result, indent=2, ensure_ascii=False)
    print(serialized)
    if args.json_report:
        args.json_report.parent.mkdir(parents=True, exist_ok=True)
        args.json_report.write_text(serialized+'\n', encoding='utf-8')
    if result['status'] != 'PASS':
        print('BLOCKED: frozen engine NOT started', file=sys.stderr)
        return 2
    if args.command:
        command = list(args.command)
        if command and command[0] == '--': command.pop(0)
        if not command:
            print('ERROR: --exec must contain command', file=sys.stderr)
            return 2
        return subprocess.run(command, check=False).returncode
    return 0


if __name__ == '__main__':
    sys.exit(main())
