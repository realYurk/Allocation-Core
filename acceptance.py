#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Production-readiness REHEARSAL (synthetic), not a certification of a real database.

Python standard library, engine source/formulas untouched.
Run with `python3 acceptance.py --mode all --workspace ./acceptance_runs`.
"""
import argparse
import concurrent.futures
import csv
from decimal import Decimal
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import shutil
import sqlite3
import subprocess
import sys
import time
import traceback
import uuid

ROOT = Path(__file__).resolve().parent
EXAMPLES = ROOT/'core'/'examples'


def exe_path():
    arch = platform.machine().lower()
    sysname = platform.system().lower()
    mapping = {
        ('windows','amd64'): 'alloc_windows_amd64.exe',
        ('windows','x86_64'): 'alloc_windows_amd64.exe',
        ('windows','arm64'): 'alloc_windows_arm64.exe',
        ('linux','x86_64'): 'alloc_linux_amd64',
        ('linux','amd64'): 'alloc_linux_amd64',
        ('linux','aarch64'): 'alloc_linux_arm64',
        ('linux','arm64'): 'alloc_linux_arm64',
        ('darwin','x86_64'): 'alloc_darwin_amd64',
        ('darwin','arm64'): 'alloc_darwin_arm64',
    }
    name = mapping.get((sysname, arch))
    if not name:
        raise RuntimeError('unsupported native OS/CPU: {} {}. See PLATFORM_SUPPORT_CN.md'.format(sysname,arch))
    p = ROOT/'core'/name
    if not p.is_file():
        raise FileNotFoundError('engine not bundled: '+str(p))
    if sysname != 'windows':
        p.chmod(p.stat().st_mode | 0o111)
    return str(p)


def sha(p):
    h = hashlib.sha256()
    with open(p,'rb') as f:
        for b in iter(lambda:f.read(1024*1024),b''):
            h.update(b)
    return h.hexdigest()


def disk_size(p):
    return sum(x.stat().st_size for x in p.rglob('*') if x.is_file()) if p.exists() else 0


def money(s):
    return Decimal(str(s))


def execute(cmd, timeout=120, env=None):
    t=time.perf_counter()
    try:
        p=subprocess.run([str(x) for x in cmd],capture_output=True,text=True,encoding='utf-8',errors='replace',timeout=timeout,env=env)
    except subprocess.TimeoutExpired as e:
        raise AssertionError('TIMEOUT ({:.0f}s): {}'.format(timeout,cmd)) from e
    duration=round(time.perf_counter()-t,3)
    if p.returncode:
        raise AssertionError('nonzero exit {}: {}\nstdout:{}\nstderr:{}'.format(p.returncode,cmd,p.stdout[-1500:],p.stderr[-1500:]))
    return duration


def verify_output(out):
    with open(out/'run_summary.json',encoding='utf-8-sig') as f:
        s=json.load(f)
    assert s['status']=='PASS', 'engine status not PASS'
    assert money(s['input_amount']) == money(s['output_amount']), 'top level nonconservation'
    n=0
    with open(out/'reconciliation.csv',newline='',encoding='utf-8-sig') as f:
        for r in csv.DictReader(f):
            assert r['status']=='PASS' and money(r['delta'])==0, 'pool mismatch: '+str(r)
            n+=1
    assert n == int(s['pool_checks']), 'pool count mismatch'
    count=0; total=Decimal(0)
    with open(out/'results.csv', newline='',encoding='utf-8-sig') as f:
        for r in csv.DictReader(f):
            total += money(r['amount']); count+=1
    assert count == int(s['terminal_rows']), 'terminal rows mismatch'
    assert total == money(s['input_amount']), 'result sum mismatch'
    return {'rows':count,'pools':n,'amount':str(total), 'output_bytes':disk_size(out)}


def make_case(name, target, amount=None):
    src=EXAMPLES/name
    shutil.copytree(src,target)
    if amount is not None:
        f=target/'amounts.csv'
        with open(f,encoding='utf-8-sig',newline='') as h:
            reader=csv.DictReader(h); fields=reader.fieldnames; rows=list(reader)
        assert rows and 'amount' in fields
        rows[0]['amount']=str(amount)
        with open(f,'w',encoding='utf-8',newline='') as h:
            w=csv.DictWriter(h,fieldnames=fields,lineterminator='\n');w.writeheader();w.writerows(rows)
    return target/'rule.json'


def run_engine(exe,rule,out,timeout=120,extra=None):
    return execute([exe,'run',rule,out,'--workers','2','--evidence-gzip']+(extra or []),timeout)


def quick(exe,ws,timeout):
    checks=[]
    for name in ('manufacturing','dual_driver','split_merge','triple_driver'):
        rule=EXAMPLES/name/'rule.json'
        out=ws/'quick'/name
        duration=run_engine(exe,rule,out,timeout)
        checks.append(dict(case=name,seconds=duration,**verify_output(out)))
    return {'status':'PASS','cases':checks}


def scenarios(exe,ws,concurrency,timeout):
    tags=['FC_{:02d}_{:02d}'.format(i,12-i) for i in range(13)]+['ACT']
    tasks=[]
    # Every version uses a separate complete input snapshot and a new full engine run.
    for i,scenario in enumerate(tags):
        for v in (1,2):
            label='{}_WIP{:02d}'.format(scenario,v)
            inpath=ws/'scenarios'/'inputs'/label
            rule=make_case('manufacturing',inpath,Decimal(1200)+i*10+v)
            tasks.append((label,rule,ws/'scenarios'/'outputs'/label))
    def work(task):
        label,rule,out=task
        dt=run_engine(exe,rule,out,timeout)
        return {'name':label,'seconds':dt,**verify_output(out),'result_sha256':sha(out/'results.csv')}
    start=time.perf_counter()
    records=[]
    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
        futures=[pool.submit(work,x) for x in tasks]
        for f in concurrent.futures.as_completed(futures):
            records.append(f.result())
    records.sort(key=lambda x:x['name'])
    assert len(records)==28 and len({x['name'] for x in records})==28
    return {'status':'PASS','scenarios':len(tags),'versions_per_scenario':2,'full_runs':len(records),
            'parallel_jobs':concurrency,'wall_seconds':round(time.perf_counter()-start,3),
            'bytes':disk_size(ws/'scenarios'),'samples':records}


def sqlite_test(exe,ws,timeout):
    # Reuses shipped reference integration; no driver, credentials, or network needed.
    example=ROOT/'integration'/'python'/'sqlite_full_run.py'
    work=ws/'sqlite_demo'
    a=execute([sys.executable,example,'--workspace',work,'--reset-demo'],timeout)
    b=execute([sys.executable,example,'--workspace',work,'--amount','2400.00'],timeout)
    dbpath=work/'finance_demo.sqlite'
    conn=sqlite3.connect(str(dbpath))
    try:
        n=conn.execute('SELECT COUNT(*) FROM allocation_run').fetchone()[0]
        assert n==2,'Expected two FULL runs'
        before=conn.execute('''SELECT a.run_id, sum(r.amount_cents), count(*) FROM allocation_active a
             JOIN allocation_result r on a.run_id=r.run_id WHERE a.scenario='FC-DEMO' GROUP BY a.run_id''').fetchone()
        assert before and before[1]==240000 and before[2]==4, str(before)
        # Intentionally fail a publish AFTER engine produced a valid result.
        spec=importlib.util.spec_from_file_location('sqlite_full_run',example)
        module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
        last=conn.execute('SELECT run_id FROM allocation_active WHERE scenario=?',('FC-DEMO',)).fetchone()[0]
        source=work/'runs'/last
        info=module.check_financial_outputs(source/'outputs')
        modified=dict(info); modified['terminal_rows']=int(info['terminal_rows'])+1
        try:
            module.publish_sqlite(conn,source/'outputs',modified,'FAIL_'+uuid.uuid4().hex[:8],'trial_fail',{})
            raise AssertionError('fault injection did not fail')
        except RuntimeError as exc:
            assert 'rows/totals disagree' in str(exc),str(exc)
        current=conn.execute('SELECT run_id FROM allocation_active WHERE scenario=?',('FC-DEMO',)).fetchone()[0]
        assert current==last,'transaction rollback did not preserve active run'
        n2=conn.execute('SELECT COUNT(*) FROM allocation_run').fetchone()[0]
        assert n2==2,'failed run left rows in DB'
    finally:
        conn.close()
    return {'status':'PASS','published_full_runs':2,'active_total':'2400.00',
            'db_rows':4,'failed_publish_rollback':'PASS','db_bytes':dbpath.stat().st_size,
            'first_seconds':a,'second_seconds':b}


def resume(exe,ws,timeout):
    base=ws/'resume'
    rule=EXAMPLES/'manufacturing'/'rule.json'
    ckpt=base/'checkpoint'
    first=base/'first'; second=base/'replayed'
    opts=['--disk-partitions','4','--checkpoint-dir',str(ckpt)]
    dt1=run_engine(exe,rule,first,timeout,opts)
    dt2=run_engine(exe,rule,second,timeout,opts+['--resume'])
    a,b=verify_output(first),verify_output(second)
    assert a['amount']==b['amount']
    matched={}
    for name in ('results.csv','reconciliation.csv','ledger.csv.gz','source_to_pool.csv.gz','lineage_links.csv.gz'):
        if (first/name).exists() and (second/name).exists():
            matched[name]=sha(first/name)==sha(second/name)
    assert matched and all(matched.values()),str(matched)
    summary=json.loads((second/'run_summary.json').read_text(encoding='utf-8-sig'))
    assert summary.get('reused_stages')==2,summary
    return {'status':'PASS','first_seconds':dt1,'resume_seconds':dt2,'reused_stages':2,'matching_files':matched,
            'scope':'successful stages reused; forced mid-stage SIGKILL not tested'}



def http_service(exe,ws,timeout):
    import socket
    import secrets
    import urllib.request
    import urllib.error
    port_sock=socket.socket()
    port_sock.bind(('127.0.0.1',0));port=port_sock.getsockname()[1];port_sock.close()
    token=secrets.token_urlsafe(24)
    base='http://127.0.0.1:{}'.format(port)
    store=ws/'http'/'service_data'
    cmd=[exe,'serve','--root',str(EXAMPLES),'--store',str(store),
         '--listen','127.0.0.1:{}'.format(port),'--jobs','2','--token',token]
    def request(method,path,body=None,authorized=True):
        data=json.dumps(body).encode('utf-8') if body is not None else None
        head={'Content-Type':'application/json'}
        if authorized:head['Authorization']='Bearer '+token
        req=urllib.request.Request(base+path,data=data,headers=head,method=method)
        with urllib.request.urlopen(req,timeout=15) as res:
            raw=res.read()
        return json.loads(raw.decode('utf-8'))
    def start():
        proc=subprocess.Popen(cmd,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
        for _ in range(70):
            if proc.poll() is not None:
                msg=proc.stderr.read(2000).decode('utf-8',errors='replace')
                raise AssertionError('HTTP process failed to start: '+msg)
            try:
                if request('GET','/healthz',authorized=False).get('status')=='ok':
                    return proc
            except Exception:
                time.sleep(.1)
        raise AssertionError('HTTP server readiness timeout')
    def stop(proc):
        if proc is None:return
        if proc.poll() is None:
            proc.terminate()
            try:proc.wait(timeout=8)
            except subprocess.TimeoutExpired:
                proc.kill();proc.wait(timeout=5)
        if proc.stderr:proc.stderr.close()
    proc=None
    try:
        proc=start()
        try:
            request('GET','/v1/jobs',authorized=False)
            raise AssertionError('unauthorized API unexpectedly succeeded')
        except urllib.error.HTTPError as e:
            assert e.code==401
        ids=[]
        for rule in ('manufacturing/rule.json','dual_driver/rule.json','split_merge/rule.json'):
            job=request('POST','/v1/jobs',{'rule':rule,'workers':2,'disk_partitions':4,'evidence_gzip':True})
            ids.append(job['id'])
        start_time=time.monotonic()
        states={}
        while time.monotonic()-start_time < timeout:
            states={job_id:request('GET','/v1/jobs/'+job_id)['status'] for job_id in ids}
            if all(v in ('PASS','FAILED','INTERRUPTED') for v in states.values()):break
            time.sleep(.15)
        assert len(states)==3 and all(v=='PASS' for v in states.values()),states
        stored={}
        for job_id in ids:
            output=store/'jobs'/job_id
            record=verify_output(output)
            stored[job_id]={'sha256':sha(output/'results.csv'),**record}
        stop(proc);proc=None
        proc=start() # verify persistence across a service restart
        for job_id in ids:
            res=request('GET','/v1/jobs/'+job_id)
            assert res['status']=='PASS','history lost after restart'
        return {'status':'PASS','submitted_jobs':3,'max_server_jobs':2,
                'authorization':'401 for no token','restarted_history':'PASS','results':stored}
    finally:
        stop(proc)

def capacity(exe,ws):
    safe=EXAMPLES/'capacity_safe_budget.json'
    blocked=EXAMPLES/'capacity_block_budget.json'
    execute([exe,'capacity',safe,ws/'capacity'/'safe'])
    cmd=[exe,'capacity',blocked,ws/'capacity'/'blocked']
    p=subprocess.run([str(x) for x in cmd],capture_output=True,text=True)
    assert p.returncode!=0,'unsafe budget unexpectedly allowed'
    return {'status':'PASS','safe_budget':'PASS','unsafe_budget':'BLOCKED','note':'soft admission only; not OS quota'}


def large(exe,ws,timeout):
    gen=ROOT/'core'/'bench'/'generate_case.py'
    case=ws/'large'/'data';case.mkdir(parents=True,exist_ok=True)
    execute([sys.executable,gen,case],timeout)
    out=ws/'large'/'output'
    sec=run_engine(exe,case/'rule.json',out,timeout)
    record=verify_output(out)
    assert record['rows']==1200000
    return {'status':'PASS','seconds':sec,**record}


def main():
    p=argparse.ArgumentParser(description='Synthetic production-readiness rehearsal. Engine unchanged.')
    p.add_argument('--mode',choices=['quick','scenarios','sqlite','resume','capacity','http','all','million'],default='quick')
    p.add_argument('--workspace',type=Path,default=ROOT/'acceptance_runs')
    p.add_argument('--concurrency',type=int,default=3)
    p.add_argument('--timeout',type=int,default=180)
    a=p.parse_args()
    if not 1<=a.concurrency<=8:p.error('concurrency 1..8')
    if not 10<=a.timeout<=3600:p.error('timeout 10..3600')
    base=a.workspace.resolve();base.mkdir(parents=True,exist_ok=True)
    from datetime import datetime, timezone
    ws=base/('session_'+datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S')+'_'+uuid.uuid4().hex[:6])
    ws.mkdir(parents=True,exist_ok=False)
    exe=exe_path()
    modes={'all':['quick','scenarios','sqlite','resume','capacity','http'],
           'million':['million']}.get(a.mode,[a.mode])
    handlers={'quick':lambda:quick(exe,ws,a.timeout),
              'scenarios':lambda:scenarios(exe,ws,a.concurrency,a.timeout),
              'sqlite':lambda:sqlite_test(exe,ws,a.timeout),
              'resume':lambda:resume(exe,ws,a.timeout),
              'capacity':lambda:capacity(exe,ws),
              'http':lambda:http_service(exe,ws,a.timeout),
              'million':lambda:large(exe,ws,max(600,a.timeout))}
    report={'engine_sha256':sha(exe),'platform':platform.platform(),
            'python':platform.python_version(),'workspace':str(ws),'started_utc':__import__('datetime').datetime.now(__import__('datetime').timezone.utc).isoformat(),
            'kind':'SYNTHETIC_REHEARSAL_NOT_REAL_PRODUCTION_DB','checks':{}}
    t=time.perf_counter()
    ok=True
    for mode in modes:
        print('CHECK',mode,flush=True)
        try:
            report['checks'][mode]=handlers[mode]()
            print('PASS',mode,flush=True)
        except BaseException as e:
            ok=False
            report['checks'][mode]={'status':'FAIL','error':str(e),'traceback':traceback.format_exc()[-5000:]}
            print('FAIL',mode,str(e),file=sys.stderr,flush=True)
    report['wall_seconds']=round(time.perf_counter()-t,3)
    report['overall']='PASS' if ok else 'FAIL'
    (ws/'acceptance_report.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
    (base/'latest_acceptance_report.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
    print('REPORT',ws/'acceptance_report.json','OVERALL',report['overall'])
    if not ok:sys.exit(1)


if __name__=='__main__': main()
