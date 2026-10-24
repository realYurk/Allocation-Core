# Python 3.8+ standard library only. No calculation logic duplicated here.
import json
import time
import urllib.request
import os
import re
import tempfile
from pathlib import Path

ALLOWED_FILES = frozenset((
    "results.csv", "ledger.csv", "ledger.csv.gz", "reconciliation.csv", "source_to_pool.csv",
    "source_to_pool.csv.gz", "lineage_links.csv", "lineage_links.csv.gz",
    "evidence_stream_manifest.json", "run_summary.json"))
JOB_ID = re.compile(r"[A-Za-z0-9_-]{1,128}\Z")

class AllocationClient:
    def __init__(self, base='http://127.0.0.1:8787', token=''):
        self.base = base.rstrip('/')
        self.token = token

    def request(self, method, path, body=None, timeout=60):
        data = json.dumps(body, ensure_ascii=False).encode('utf-8') if body is not None else None
        headers = {'Accept':'application/json'}
        if data is not None: headers['Content-Type'] = 'application/json; charset=utf-8'
        if self.token: headers['Authorization'] = 'Bearer ' + self.token
        req = urllib.request.Request(self.base + path, data=data, headers=headers, method=method)
        with urllib.request.urlopen(req, timeout=timeout) as res:
            return json.loads(res.read().decode('utf-8'))

    def submit(self, rule='manufacturing/rule.json'):
        return self.request('POST','/v1/jobs',{'rule':rule,'workers':2,'disk_partitions':4,'evidence_gzip':True})

    def wait(self, id, timeout=180, interval=0.3):
        end = time.monotonic()+timeout
        while time.monotonic()<end:
            job = self.request('GET','/v1/jobs/'+id)
            if job['status'] in ('PASS','FAILED','INTERRUPTED'):
                return job
            time.sleep(interval)
        raise TimeoutError('Job still active; query status later by id: '+id)

    def stream_result(self, id, path, destination):
        if not JOB_ID.fullmatch(id):
            raise ValueError('Invalid job id')
        if path not in ALLOWED_FILES:
            raise ValueError('Unsupported endpoint filename')
        destination = Path(destination)
        destination.parent.mkdir(parents=True, exist_ok=True)
        headers = {}
        if self.token: headers['Authorization'] = 'Bearer ' + self.token
        req = urllib.request.Request(self.base+'/v1/jobs/'+id+'/'+path,headers=headers)
        temp_path = None
        try:
            with urllib.request.urlopen(req, timeout=120) as src:
                expected = src.headers.get('Content-Length')
                byte_count = 0
                with tempfile.NamedTemporaryFile(mode='wb', dir=str(destination.parent),
                                             prefix='.allocation-download-', suffix='.part', delete=False) as dst:
                    temp_path = dst.name
                    while True:
                        block = src.read(1024 * 1024)
                        if not block: break
                        byte_count += len(block)
                        dst.write(block)
                if expected is not None and byte_count != int(expected):
                    raise IOError('Truncated download: expected=%s actual=%s' % (expected, byte_count))
            # Only publish the output once the entire HTTP response was transferred.
            os.replace(temp_path, destination)
            temp_path = None
        finally:
            if temp_path is not None and os.path.exists(temp_path):
                os.unlink(temp_path)

if __name__=='__main__':
    c = AllocationClient()
    job = c.submit()
    result = c.wait(job['id'])
    print(json.dumps(result,ensure_ascii=False,indent=2))
    if result['status']!='PASS': raise SystemExit(1)
