#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Opt-in integration test, writes data to the database selected by ALLOC_DB_DSN.
Requires an isolated TEST database with tables created from sql/*_template.sql.
No automatic test database cleanup; all runs and artifacts retained for audit.
"""
import argparse
import json
from pathlib import Path
import os
import sys
import time
from datetime import datetime, timezone
from decimal import Decimal
import uuid

import acceptance as tools
from integration.python import sql_bulk_publish as publisher


def inspect(db, dsn, scenario, version, run_id, expected, expected_rows):
    if db == 'postgres':
        import psycopg
        with psycopg.connect(dsn) as con:
            with con.cursor() as c:
                c.execute('SELECT active_run_id FROM allocation_active WHERE scenario=%s AND business_version=%s',(scenario,version))
                a=c.fetchone()
                c.execute('SELECT COUNT(*),COALESCE(SUM(amount),0) FROM allocation_result WHERE run_id=%s',(run_id,))
                v=c.fetchone()
    else:
        import pyodbc
        con=pyodbc.connect(dsn)
        try:
            c=con.cursor()
            c.execute('SELECT active_run_id FROM dbo.allocation_active WHERE scenario=? AND business_version=?',(scenario,version));a=c.fetchone()
            c.execute('SELECT COUNT_BIG(*),COALESCE(SUM(amount),0) FROM dbo.allocation_result WHERE run_id=?',(run_id,));v=c.fetchone()
        finally:con.close()
    assert a is not None and a[0]==run_id,'active pointer is not new run'
    assert v is not None and int(v[0])==expected_rows and Decimal(str(v[1]))==Decimal(expected),'DB totals/rows mismatch'
    return {'active_run_id':str(a[0]),'db_rows':int(v[0]),'db_amount':str(v[1])}


def main():
    ap=argparse.ArgumentParser(description='EXPLICIT WRITE test against a dedicated real TEST database only')
    ap.add_argument('--db',choices=['postgres','sqlserver'],required=True)
    ap.add_argument('--workspace',type=Path,default=Path('./acceptance_live_runs'))
    ap.add_argument('--profile',choices=['demo','million'],default='demo')
    ap.add_argument('--i-understand-this-writes-to-my-test-database',action='store_true')
    args=ap.parse_args()
    if not args.i_understand_this_writes_to_my_test_database or os.environ.get('ALLOC_TEST_ALLOW_DB_WRITES')!='YES':
        ap.error('Set ALLOC_TEST_ALLOW_DB_WRITES=YES to authorize persistent writes in a dedicated test DB')
    dsn=os.environ.get('ALLOC_DB_DSN')
    if not dsn:ap.error('ALLOC_DB_DSN missing; credentials must be in environment, not committed')
    ws=(args.workspace.resolve()/('session_'+datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S')+'_'+uuid.uuid4().hex[:6]));ws.mkdir(parents=True,exist_ok=False)
    ts=datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S')
    scenario='ACCEPTANCE_'+ts+'_'+uuid.uuid4().hex[:6]
    version='FULL'
    engine=tools.exe_path()
    records=[]
    if args.profile=='million':
        generator=tools.ROOT/'core'/'bench'/'generate_case.py'
        generated=ws/'generator_source'
        tools.execute([sys.executable,generator,generated],180)
        source_name='million'
        amounts=('5000.00','5001.00')
    else:
        source_name='manufacturing'
        amounts=('1200.00','2400.00')
    for step,amount in enumerate(amounts,1):
        name='FULL_{:02}'.format(step)
        if source_name=='million':
            import shutil
            shutil.copytree(generated,ws/name/'input')
            rule=ws/name/'input'/'rule.json'
            if step==2:
                with (ws/name/'input'/'amounts.csv').open(encoding='utf-8',newline='') as f:
                    import csv
                    reader=csv.DictReader(f);fields=reader.fieldnames;rows=list(reader)
                rows[0]['amount']=amount
                with (ws/name/'input'/'amounts.csv').open('w',encoding='utf-8',newline='') as f:
                    w=csv.DictWriter(f,fieldnames=fields,lineterminator='\n');w.writeheader();w.writerows(rows)
        else:
            rule=tools.make_case('manufacturing',ws/name/'input',Decimal(amount))
        out=ws/name/'output'
        compute=tools.run_engine(engine,rule,out,180)
        summary=publisher.check_outputs(out)
        run_id=scenario+'_'+name
        t=time.perf_counter()
        if args.db=='postgres':
            published=publisher.publish_pg(dsn,out,run_id,scenario,version,summary)
        else:
            published=publisher.publish_sqlserver(dsn,out,run_id,scenario,version,summary,5000)
        sec=round(time.perf_counter()-t,3)
        expected=summary['input_amount']
        validated=inspect(args.db,dsn,scenario,version,run_id,expected,int(summary['terminal_rows']))
        assert published==int(summary['terminal_rows'])
        records.append({'run_id':run_id,'amount':amount,'engine_seconds':compute,
                        'db_bulk_publish_seconds':sec,'output_bytes':tools.disk_size(out),**validated})
        print('PASS DB publish',run_id,'seconds',sec,flush=True)
    report={'status':'PASS','database':args.db,'scenario':scenario,'retains_both_runs':True,
            'isolated_test_db_only':True,'records':records,'profile':args.profile,
            'limits':'Check real DB transaction log, latency and index overhead separately; tests insert permanent rows.'}
    (ws/'live_db_report.json').write_text(json.dumps(report,indent=2),encoding='utf-8')
    print('REPORT',ws/'live_db_report.json')

if __name__=='__main__':main()
