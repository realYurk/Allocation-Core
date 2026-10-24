# -*- coding: utf-8 -*-
"""Integration-only regression: capacity guard, replay source snapshot, downloads."""
import importlib.util
import json
import subprocess
import sys
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
TOOL = ROOT / 'integration/tools/capacity_preflight.py'
CAPTURE = ROOT / 'integration/tools/capture_replay_inputs.py'
CLIENT_FILE = ROOT / 'integration/python/http_client.py'
spec = importlib.util.spec_from_file_location('http_client', CLIENT_FILE)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

class Stub(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.endswith('/ledger.csv.gz'):
            blob = b'valid compressed-file bytes stand-in'
            self.send_response(200)
            self.send_header('Content-Length',str(len(blob)))
            self.end_headers()
            self.wfile.write(blob)
        elif self.path.endswith('/lineage_links.csv.gz'):
            blob = b'truncated'
            self.send_response(200)
            self.send_header('Content-Length',str(len(blob)+10))
            self.end_headers()
            self.wfile.write(blob)
        else:
            self.send_response(404)
            self.end_headers()
    def log_message(self,*args): pass

class HardeningTest(unittest.TestCase):
    def test_preflight_blocks_huge_without_launch(self):
        with tempfile.TemporaryDirectory() as d:
            sentinel = Path(d)/'NEVER_RUN'
            proc = subprocess.run([sys.executable,str(TOOL),'--pools','3600','--targets-per-pool','500000',
                                   '--disk-path',d,'--exec',sys.executable,'-c',
                                   "from pathlib import Path; Path(%r).touch()"%str(sentinel)],
                                   text=True,capture_output=True)
            self.assertEqual(proc.returncode,2,proc.stdout+proc.stderr)
            self.assertIn('1800000000',proc.stdout)
            self.assertFalse(sentinel.exists())

    def test_preflight_allows_small_launch(self):
        with tempfile.TemporaryDirectory() as d:
            sentinel = Path(d)/'RAN'
            proc = subprocess.run([sys.executable,str(TOOL),'--pools','3','--targets-per-pool','4',
                                   '--reserve-free-gib','0.01','--disk-path',d,
                                   '--exec',sys.executable,'-c',
                                   "from pathlib import Path; Path(%r).touch()"%str(sentinel)],
                                   text=True,capture_output=True)
            self.assertEqual(proc.returncode,0,proc.stdout+proc.stderr)
            self.assertTrue(sentinel.exists())

    def test_complete_inputs_then_tamper(self):
        with tempfile.TemporaryDirectory() as d:
            snap=Path(d)/'snapshot'
            rule=ROOT/'core/examples/manufacturing/rule.json'
            cmd=[sys.executable,str(CAPTURE),'--root',str(rule.parent),'--rule',str(rule),'--output',str(snap)]
            first=subprocess.run(cmd,capture_output=True,text=True)
            self.assertEqual(first.returncode,0,first.stderr)
            self.assertEqual(subprocess.run([sys.executable,str(CAPTURE),'--verify',str(snap)],capture_output=True).returncode,0)
            (snap/'files'/'labor.csv').write_text('invalid change',encoding='utf-8')
            self.assertEqual(subprocess.run([sys.executable,str(CAPTURE),'--verify',str(snap)],capture_output=True).returncode,2)
            self.assertEqual(subprocess.run(cmd,capture_output=True).returncode,2)

    def test_python_sdk_exact_endpoints_and_atomicity(self):
        server=ThreadingHTTPServer(('127.0.0.1',0),Stub)
        thread=threading.Thread(target=server.serve_forever,daemon=True)
        thread.start()
        try:
            c=mod.AllocationClient('http://127.0.0.1:'+str(server.server_port))
            self.assertEqual(len(mod.ALLOWED_FILES),10)
            with tempfile.TemporaryDirectory() as d:
                target=Path(d)/'evidence.gz'
                c.stream_result('job_123','ledger.csv.gz',target)
                self.assertEqual(target.read_bytes(),b'valid compressed-file bytes stand-in')
                for fn,ex in [('lineage_links.csv.gz',OSError),('../passwd',ValueError)]:
                    with self.assertRaises(ex): c.stream_result('job_123',fn,target)
                    self.assertEqual(target.read_bytes(),b'valid compressed-file bytes stand-in')
                self.assertFalse(list(Path(d).glob('*.part')))
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)

if __name__=='__main__': unittest.main()
