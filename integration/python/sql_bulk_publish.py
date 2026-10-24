#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Optional PostgreSQL/SQL Server bulk publisher for validated FULL runs.

This integration code DOES NOT compute allocations. PostgreSQL needs psycopg 3;
SQL Server needs pyodbc + ODBC Driver. Database integration has not been
validated against a live enterprise instance in this distribution.

Credentials are supplied only via ALLOC_DB_DSN environment variable.
Old runs are immutable: new run loads first, then current pointer switches
inside the same transaction, rollback on any mismatch.
"""
import argparse
import csv
from decimal import Decimal, InvalidOperation
import hashlib
import json
import os
from pathlib import Path
import sys


def exact_amount(s):
    try:
        d = Decimal(str(s))
    except InvalidOperation as exc:
        raise ValueError('invalid financial decimal') from exc
    if not d.is_finite() or d.as_tuple().exponent < -6 or len(d.as_tuple().digits) > 38:
        raise ValueError('amount must be finite and fit NUMERIC(38,6)')
    return d


def sha256(path):
    h = hashlib.sha256()
    with open(path, 'rb') as f:
        for b in iter(lambda: f.read(1024 * 1024), b''):
            h.update(b)
    return h.hexdigest()


def input_fingerprint(summary):
    """Digest of the canonical map of SHA-256 values for ORIGINAL input CSVs.

    It is NOT a hash of results.csv and not a bitwise concatenation of inputs.
    Every input still has its individual digest in run_summary.data_sha256.
    """
    import re
    entries = summary.get('data_sha256')
    if not isinstance(entries, dict) or not entries:
        raise ValueError('run summary has no original input SHA-256 map')
    for name, digest in entries.items():
        if not isinstance(name, str) or not isinstance(digest, str) or not re.fullmatch(r'[0-9a-f]{64}', digest):
            raise ValueError('invalid original input SHA-256 map')
    raw=json.dumps(entries, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode('utf-8')
    return hashlib.sha256(raw).hexdigest()


def check_outputs(out):
    summary = json.loads((out / 'run_summary.json').read_text('utf-8-sig'))
    if summary.get('status') != 'PASS':
        raise ValueError('engine summary not PASS')
    total = exact_amount(summary['input_amount'])
    if total != exact_amount(summary['output_amount']):
        raise ValueError('engine top-level totals differ')
    checks = 0
    with (out / 'reconciliation.csv').open(newline='', encoding='utf-8-sig') as f:
        for r in csv.DictReader(f):
            checks += 1
            if r['status'] != 'PASS' or exact_amount(r['delta']) != 0:
                raise ValueError('pool reconciliation failed')
    if checks != int(summary['pool_checks']):
        raise ValueError('missing pool checks')
    if int(summary['terminal_rows']) < 1:
        raise ValueError('empty terminal results')
    return summary


def rows(path, run_id):
    with path.open(encoding='utf-8-sig', newline='') as f:
        rdr = csv.DictReader(f)
        if not rdr.fieldnames or len(rdr.fieldnames) != len(set(rdr.fieldnames)):
            raise ValueError('invalid or duplicate columns in results.csv')
        required = {'row_id', 'terminal_stage', 'amount'}
        if not required.issubset(rdr.fieldnames):
            raise ValueError('results.csv lacks required fields')
        dims = [name for name in rdr.fieldnames if name not in required]
        for r in rdr:
            if not r['row_id'] or not r['terminal_stage']:
                raise ValueError('empty row identity/stage')
            # Exact decimal strings; never pass money through float.
            yield (run_id, r['terminal_stage'], r['row_id'], exact_amount(r['amount']),
                   json.dumps({k: r[k] for k in dims}, ensure_ascii=False, separators=(',', ':')))


def publish_pg(dsn, out, run_id, scenario, business_version, summary):
    import psycopg  # optional; installed by the consuming application
    from psycopg.types.json import Jsonb
    with psycopg.connect(dsn) as conn:
        with conn.cursor() as cur:
            cur.execute('''INSERT INTO allocation_run
              (run_id,scenario,business_version,source_sha256,rule_sha256,
               expected_amount,expected_rows,status)
              VALUES (%s,%s,%s,%s,%s,%s,%s,%s)''',
              (run_id, scenario, business_version, input_fingerprint(summary),
               summary['rule_sha256'], exact_amount(summary['input_amount']),
               int(summary['terminal_rows']), 'VALIDATED'))
            with cur.copy('''COPY allocation_result
              (run_id,terminal_stage,row_id,amount,dimensions_json) FROM STDIN''') as cp:
                for run,stage,rid,amount,dims in rows(out/'results.csv',run_id):
                    cp.write_row((run,stage,rid,amount,Jsonb(json.loads(dims))))
            cur.execute('''SELECT COUNT(*),COALESCE(SUM(amount),0)
                           FROM allocation_result WHERE run_id=%s''',(run_id,))
            count, money = cur.fetchone()
            if count != int(summary['terminal_rows']) or money != exact_amount(summary['input_amount']):
                raise ValueError('database row count or amount mismatch: transaction rolled back')
            cur.execute('''INSERT INTO allocation_active(scenario,business_version,active_run_id)
              VALUES (%s,%s,%s) ON CONFLICT(scenario,business_version)
              DO UPDATE SET active_run_id=EXCLUDED.active_run_id''',
              (scenario,business_version,run_id))
        # commit only after pointer and all validation succeed
    return int(summary['terminal_rows'])


def publish_sqlserver(dsn, out, run_id, scenario, business_version, summary, batch_size):
    import pyodbc  # optional; installed by the consuming application
    con = pyodbc.connect(dsn, autocommit=False)
    try:
        cur = con.cursor()
        cur.execute('''INSERT INTO dbo.allocation_run
          (run_id,scenario,business_version,expected_amount,expected_rows,status)
          VALUES (?,?,?,?,?,?)''',
          (run_id,scenario,business_version,str(exact_amount(summary['input_amount'])),
           int(summary['terminal_rows']),'VALIDATED'))
        cur.fast_executemany = True
        buf = []
        stmt = '''INSERT INTO dbo.allocation_result
          (run_id,terminal_stage,row_id,amount,dimensions_json) VALUES (?,?,?,?,?)'''
        for record in rows(out/'results.csv',run_id):
            # Decimal is sent as string to avoid ODBC driver coercion to binary float.
            a = list(record)
            a[3] = str(a[3])
            buf.append(tuple(a))
            if len(buf) >= batch_size:
                cur.executemany(stmt,buf)
                buf.clear()
        if buf:
            cur.executemany(stmt,buf)
        cur.execute('''SELECT COUNT_BIG(*),COALESCE(SUM(amount),0)
                       FROM dbo.allocation_result WHERE run_id=?''',run_id)
        count, money = cur.fetchone()
        if count != int(summary['terminal_rows']) or Decimal(str(money)) != exact_amount(summary['input_amount']):
            raise ValueError('database row count or amount mismatch: transaction rolled back')
        # Acquire serializable key-range lock: no torn active pointer publication.
        cur.execute('''UPDATE dbo.allocation_active WITH (UPDLOCK,SERIALIZABLE)
                       SET active_run_id=? WHERE scenario=? AND business_version=?''',
                    run_id,scenario,business_version)
        if cur.rowcount == 0:
            cur.execute('''INSERT INTO dbo.allocation_active
                       (scenario,business_version,active_run_id) VALUES (?,?,?)''',
                       scenario,business_version,run_id)
        con.commit()
        return int(summary['terminal_rows'])
    except BaseException:
        con.rollback()
        raise
    finally:
        con.close()


def main():
    p = argparse.ArgumentParser(description='Publish one validated FULL allocation run to an existing database')
    p.add_argument('--db', choices=['postgres','sqlserver'], required=True)
    p.add_argument('--output-dir', type=Path, required=True)
    p.add_argument('--run-id', required=True)
    p.add_argument('--scenario', required=True)
    p.add_argument('--business-version', required=True)
    p.add_argument('--batch-size', type=int, default=5000)
    p.add_argument('--check-only', action='store_true')
    a = p.parse_args()
    for name in ('run_id','scenario','business_version'):
        v = getattr(a,name)
        if not v or len(v)>100 or not all(c.isascii() and (c.isalnum() or c in '._-+') for c in v):
            p.error(name+' must be 1..100 safe ASCII chars')
    if not 100 <= a.batch_size <= 50000:
        p.error('batch-size must be 100..50000')
    out = a.output_dir.resolve()
    summary = check_outputs(out)
    if a.check_only:
        print(json.dumps({'validated':True,'expected_rows':summary['terminal_rows'],
            'input_amount':summary['input_amount'],'result_sha256':sha256(out/'results.csv')},indent=2))
        return
    dsn = os.environ.get('ALLOC_DB_DSN')
    if not dsn:
        p.error('set ALLOC_DB_DSN in environment; never embed credentials into code')
    if a.db == 'postgres':
        n = publish_pg(dsn,out,a.run_id,a.scenario,a.business_version,summary)
    else:
        n = publish_sqlserver(dsn,out,a.run_id,a.scenario,a.business_version,summary,a.batch_size)
    print(json.dumps({'committed':True,'database':a.db,'rows':n,'run_id':a.run_id}))

if __name__ == '__main__':
    try:
        main()
    except Exception as e:
        print('ERROR: '+str(e),file=sys.stderr)
        sys.exit(1)
