# -*- coding: utf-8 -*-
"""Actual Go core + SQLite end-to-end and rollback integration regression."""
import importlib.util
import json
import sqlite3
import subprocess
import sys
import tempfile
from pathlib import Path

KIT = Path(__file__).resolve().parents[1]
SCRIPT=KIT/'integration/python/sqlite_full_run.py'
spec=importlib.util.spec_from_file_location('sql_demo',str(SCRIPT))
m=importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)

with tempfile.TemporaryDirectory(prefix='allocation-v21-test-') as d:
    root=Path(d)
    for args in [['--reset-demo'], ['--amount','2400.00']]:
        cmd=[sys.executable,str(SCRIPT),'--workspace',str(root)]+args
        p=subprocess.run(cmd,capture_output=True,text=True,encoding='utf-8')
        if p.returncode: raise AssertionError('integration run failed: '+p.stdout+'\n'+p.stderr)
        assert 'SUCCESS' in p.stdout
    db=sqlite3.connect(str(root/'finance_demo.sqlite'))
    try:
        assert db.execute('select count(*) from allocation_run').fetchone()[0]==2
        active_before=db.execute("SELECT run_id FROM allocation_active WHERE scenario='FC-DEMO'").fetchone()[0]
        by_sku={r[0]:r[1] for r in db.execute('SELECT sku,amount_cents FROM allocation_result WHERE run_id=?',(active_before,))}
        assert by_sku=={'商品A':20000,'商品B':60000,'商品C':80000,'商品D':80000}, by_sku
        prior=[r[0] for r in db.execute('SELECT run_id FROM allocation_run WHERE run_id<>?',(active_before,))][0]
        assert db.execute('SELECT SUM(amount_cents) FROM allocation_result WHERE run_id=?',(prior,)).fetchone()[0]==120000
        output=root/'runs'/active_before/'outputs'
        current_summary=m.check_financial_outputs(output)
        bad=dict(current_summary)
        bad['terminal_rows']=int(bad['terminal_rows'])+1
        try:
            m.publish_sqlite(db,output,bad,'intentional_bad_test','bad',{'rule.json':'abc'})
        except RuntimeError as e:
            assert 'DB import rows/totals disagree' in str(e)
        else:
            raise AssertionError('bad import should reject')
        assert db.execute("SELECT run_id FROM allocation_active WHERE scenario='FC-DEMO'").fetchone()[0]==active_before
        assert db.execute("SELECT count(*) FROM allocation_run WHERE run_id='intentional_bad_test'").fetchone()[0]==0
        print('PASS: SQLite full run twice, active pointer, historical rows, rollback on wrong expected count')
    finally:
        db.close()
