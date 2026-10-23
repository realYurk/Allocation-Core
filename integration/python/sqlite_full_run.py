# -*- coding: utf-8 -*-
"""An end-to-end SQL -> Allocation Core -> SQL integration demonstration.

Python standard library only. The authoritative V20 Go engine is NOT modified.
SQLite is a teaching and integration-contract validation database, NOT a
recommendation for a 10M-row multi-tenant production warehouse.

Usage:
  python integration/python/sqlite_full_run.py --workspace my_demo --reset-demo
  python integration/python/sqlite_full_run.py --workspace my_demo --amount 2400.00
  python integration/python/sqlite_full_run.py --workspace my_demo --show-only
"""
import argparse
import csv
import hashlib
import json
import os
import sqlite3
import subprocess
import sys
import uuid
from decimal import Decimal, InvalidOperation
from pathlib import Path
from datetime import datetime, timezone

ROOT = Path(__file__).resolve().parents[2]
CORE = ROOT / 'core'
EXAMPLE = CORE / 'examples' / 'manufacturing'


def money_cents(s):
    """Validate exact cents; never pass financial amounts through float."""
    try:
        d = Decimal(str(s))
    except InvalidOperation as e:
        raise ValueError('invalid financial amount: ' + str(s)) from e
    if not d.is_finite() or d != d.quantize(Decimal('0.01')):
        raise ValueError('amount must have at most two decimals: ' + str(s))
    cents = int(d * 100)
    if abs(cents) > (2**63 - 1):
        raise ValueError('amount exceeds int64 cents')
    return cents


def hash_file(path):
    h = hashlib.sha256()
    with open(path, 'rb') as f:
        for buf in iter(lambda: f.read(1024*1024), b''):
            h.update(buf)
    return h.hexdigest()


def locate_engine():
    if sys.platform.startswith('win'):
        import platform
        name = 'alloc_windows_arm64.exe' if platform.machine().lower() in ('arm64','aarch64') else 'alloc_windows_amd64.exe'
    elif sys.platform == 'darwin':
        import platform
        name = 'alloc_darwin_arm64' if platform.machine().lower() in ('arm64','aarch64') else 'alloc_darwin_amd64'
    else:
        name = 'alloc_linux_amd64'
    exe = CORE / name
    if not exe.exists():
        raise RuntimeError('Cannot find allocation engine: ' + str(exe))
    if os.name != 'nt' and not os.access(str(exe), os.X_OK):
        exe.chmod(exe.stat().st_mode | 0o100)  # ZIP extractors may strip Unix executable bit
    return str(exe)


def csv_rows(path):
    with open(path, 'r', encoding='utf-8-sig', newline='') as f:
        return list(csv.DictReader(f))


def initialize(db):
    db.executescript('''
    PRAGMA foreign_keys=ON;
    CREATE TABLE IF NOT EXISTS source_amount (
      period TEXT, plant TEXT, brand TEXT, family TEXT, sku TEXT,
      account TEXT, currency TEXT, amount TEXT, row_id TEXT PRIMARY KEY);
    CREATE TABLE IF NOT EXISTS source_labor (
      period TEXT, plant TEXT, family TEXT, hours TEXT);
    CREATE TABLE IF NOT EXISTS source_sales (
      period TEXT, plant TEXT, family TEXT, sku TEXT, units TEXT);
    CREATE TABLE IF NOT EXISTS allocation_run (
      run_id TEXT PRIMARY KEY, scenario TEXT NOT NULL, version TEXT NOT NULL,
      source_sha256 TEXT NOT NULL, rule_sha256 TEXT NOT NULL,
      result_sha256 TEXT NOT NULL, amount_cents INTEGER NOT NULL,
      output_rows INTEGER NOT NULL, created_utc TEXT NOT NULL, status TEXT NOT NULL);
    CREATE TABLE IF NOT EXISTS allocation_result (
      run_id TEXT NOT NULL, row_id TEXT NOT NULL,
      terminal_stage TEXT NOT NULL,
      period TEXT, plant TEXT, brand TEXT, family TEXT, sku TEXT,
      account TEXT, currency TEXT,
      amount_text TEXT NOT NULL, amount_cents INTEGER NOT NULL,
      PRIMARY KEY(run_id, terminal_stage, row_id),
      FOREIGN KEY(run_id) REFERENCES allocation_run(run_id));
    CREATE INDEX IF NOT EXISTS idx_result_lookup ON allocation_result(run_id, account, period, sku);
    CREATE TABLE IF NOT EXISTS allocation_active (
      scenario TEXT PRIMARY KEY, run_id TEXT NOT NULL REFERENCES allocation_run(run_id));
    ''')


def reset_sources(db):
    for table, file, cols in [
        ('source_amount','amounts.csv','period plant brand family sku account currency amount row_id'.split()),
        ('source_labor','labor.csv','period plant family hours'.split()),
        ('source_sales','sales.csv','period plant family sku units'.split())]:
        with db:
            db.execute('DELETE FROM ' + table)
            rows = csv_rows(EXAMPLE/file)
            db.executemany('INSERT INTO '+table+' ('+','.join(cols)+') VALUES ('+','.join('?' for c in cols)+')',
                           [tuple(r[c] for c in cols) for r in rows])


def write_source(db, table, cols, dest, order):
    dest.parent.mkdir(parents=True, exist_ok=True)
    with open(dest, 'w', newline='', encoding='utf-8-sig') as f:
        writer = csv.writer(f, lineterminator='\n')
        writer.writerow(cols)
        sql = 'SELECT '+','.join(cols)+' FROM '+table+' ORDER BY '+','.join(order)
        for row in db.execute(sql):
            writer.writerow(row)


def export_snapshot(db, directory):
    source = directory/'input'
    write_source(db,'source_amount','period plant brand family sku account currency amount row_id'.split(),source/'amounts.csv',['row_id'])
    write_source(db,'source_labor','period plant family hours'.split(),source/'labor.csv',['period','plant','family'])
    write_source(db,'source_sales','period plant family sku units'.split(),source/'sales.csv',['period','plant','family','sku'])
    (source/'rule.json').write_bytes((EXAMPLE/'rule.json').read_bytes())
    hashes = {p.name:hash_file(p) for p in sorted(source.iterdir())}
    (directory/'inputs_sha256.json').write_text(json.dumps(hashes,indent=2),encoding='utf-8')
    return source/'rule.json', hashes


def check_financial_outputs(out):
    summary = json.loads((out/'run_summary.json').read_text(encoding='utf-8-sig'))
    if summary['status'] != 'PASS':
        raise RuntimeError('engine did not finish PASS')
    if money_cents(summary['input_amount']) != money_cents(summary['output_amount']):
        raise RuntimeError('engine top-level totals differ')
    count = 0
    with open(out/'reconciliation.csv',newline='',encoding='utf-8-sig') as f:
        for r in csv.DictReader(f):
            count += 1
            if r['status'] != 'PASS' or money_cents(r['delta']) != 0:
                raise RuntimeError('reconciliation failed in pool: ' + r['pool'])
    if count != summary['pool_checks']:
        raise RuntimeError('reconciliation count differs from summary')
    return summary


def publish_sqlite(db, out, summary, run_id, version, hashes):
    """Full new run, atomic publication. Rollback leaves prior active run intact."""
    before = money_cents(summary['input_amount'])
    expected_rows = int(summary['terminal_rows'])
    sha = hash_file(out/'results.csv')
    now = datetime.now(timezone.utc).isoformat()
    try:
        db.execute('BEGIN IMMEDIATE')
        db.execute('INSERT INTO allocation_run VALUES(?,?,?,?,?,?,?,?,?,?)', (
            run_id,'FC-DEMO',version,hashlib.sha256(json.dumps(hashes,sort_keys=True).encode()).hexdigest(),
            summary['rule_sha256'],sha,before,expected_rows,now,'VALIDATED'))
        actual_rows = 0
        actual_sum = 0
        with open(out/'results.csv',encoding='utf-8-sig',newline='') as f:
            for r in csv.DictReader(f):
                cents = money_cents(r['amount'])
                actual_sum += cents
                actual_rows += 1
                db.execute('''INSERT INTO allocation_result
                  (run_id,row_id,terminal_stage,period,plant,brand,family,sku,account,currency,amount_text,amount_cents)
                  VALUES(?,?,?,?,?,?,?,?,?,?,?,?)''',(
                     run_id,r['row_id'],r['terminal_stage'],r['period'],r['plant'],r['brand'],r['family'],r['sku'],
                     r['account'],r['currency'],r['amount'],cents))
        if actual_rows != expected_rows or actual_sum != before:
            raise RuntimeError('DB import rows/totals disagree: rows={} != {}, sum={} != {}'.format(actual_rows,expected_rows,actual_sum,before))
        # Never edit or delete old successful data in place. Switch just the active pointer.
        db.execute('INSERT OR REPLACE INTO allocation_active(scenario,run_id) VALUES (?,?)',('FC-DEMO',run_id))
        db.commit()
    except Exception:
        db.rollback()
        raise
    return actual_rows, actual_sum


def report(db):
    print('\nCurrent effective scenario summary (read-only SQL query):')
    q = '''SELECT r.account,r.period,r.sku,sum(r.amount_cents)
           FROM allocation_result r JOIN allocation_active a ON a.run_id=r.run_id
           WHERE a.scenario=? GROUP BY r.account,r.period,r.sku ORDER BY r.account,r.period,r.sku'''
    for acc,period,sku,cents in db.execute(q,('FC-DEMO',)):
        print('  {} | {} | {} | {:.2f}'.format(acc,period,sku,Decimal(cents)/Decimal(100)))
    print('Historical runs:', db.execute('SELECT count(*) FROM allocation_run').fetchone()[0])
    print('Active:',db.execute('SELECT run_id FROM allocation_active WHERE scenario=?',('FC-DEMO',)).fetchone()[0])


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--workspace',default=str(ROOT/'demo_workspace'))
    parser.add_argument('--reset-demo',action='store_true')
    parser.add_argument('--amount',default=None,help='replace source EXP-001 amount before a new FULL calculation')
    parser.add_argument('--show-only',action='store_true')
    a = parser.parse_args()
    root = Path(a.workspace).resolve()
    root.mkdir(parents=True,exist_ok=True)
    db = sqlite3.connect(str(root/'finance_demo.sqlite'))
    try:
        initialize(db)
        if a.reset_demo:
            reset_sources(db)
        if a.show_only:
            report(db)
            return
        if a.amount is not None:
            money_cents(a.amount)
            with db:
                db.execute('UPDATE source_amount SET amount=? WHERE row_id=?',(a.amount,'EXP-001'))
        if db.execute('SELECT count(*) FROM source_amount').fetchone()[0] == 0:
            reset_sources(db)
        run_id = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S')+'_'+uuid.uuid4().hex[:8]
        version = 'trial_'+run_id
        directory = root/'runs'/run_id
        rule, hashes = export_snapshot(db,directory)
        exe = locate_engine()
        for command in [[exe,'doctor',str(rule)],[exe,'validate',str(rule)]]:
            p = subprocess.run(command,capture_output=True,text=True,encoding='utf-8',errors='replace')
            if p.returncode:
                raise RuntimeError('Preflight failed: '+' '.join(command)+'\n'+p.stderr+'\n'+p.stdout)
        out = directory/'outputs'
        cmd = [exe,'run',str(rule),str(out),'--workers','2','--disk-partitions','4','--evidence-gzip']
        p = subprocess.run(cmd,capture_output=True,text=True,encoding='utf-8',errors='replace')
        if p.returncode:
            raise RuntimeError('Core run failed: '+p.stdout+'\n'+p.stderr)
        summary = check_financial_outputs(out)
        if hash_file(out/'results.csv') == '':
            raise AssertionError('unreachable')
        rows,total = publish_sqlite(db,out,summary,run_id,version,hashes)
        print('SUCCESS. FULL recalculation complete, SQL published atomically.')
        print('Engine:',exe,'| Result rows:',rows,'| Total cents:',total)
        print('Workspace:',root)
        report(db)
    finally:
        db.close()

if __name__=='__main__':
    main()
