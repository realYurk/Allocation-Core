# -*- coding: utf-8 -*-
"""Independent attack tests for V21.1 hardening, beyond the vendor's own suite."""
import importlib.util
import os
import subprocess
import sys
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CLIENT = ROOT / 'integration/python/http_client.py'
spec = importlib.util.spec_from_file_location('hc', CLIENT)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

PASS = 0
FAIL = 0


def check(name, cond, detail=''):
    global PASS, FAIL
    if cond:
        PASS += 1
        print('PASS  ' + name)
    else:
        FAIL += 1
        print('FAIL  ' + name + ('  :: ' + str(detail) if detail else ''))


class ChunkedStub(BaseHTTPRequestHandler):
    """Serves a body WITHOUT Content-Length (chunked/streamed)."""
    def do_GET(self):
        blob = b'no-content-length-body-1234567890'
        self.send_response(200)
        # deliberately omit Content-Length
        self.end_headers()
        self.wfile.write(blob)
    def log_message(self, *a):
        pass


def test_no_content_length_behavior():
    srv = ThreadingHTTPServer(('127.0.0.1', 0), ChunkedStub)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    c = mod.AllocationClient('http://127.0.0.1:' + str(srv.server_port))
    with tempfile.TemporaryDirectory() as d:
        target = Path(d) / 'out.bin'
        try:
            c.stream_result('job_1', 'ledger.csv', target)
            # Content-Length absent -> client cannot verify, but should still write
            check('no-CL: client still writes file (documented limitation)', target.exists())
            check('no-CL: no .part leftover', not list(Path(d).glob('*.part')))
        except Exception as e:
            check('no-CL: client still writes file (documented limitation)', False, repr(e))
    srv.shutdown(); srv.server_close()


def test_python_vs_java_whitelist_parity():
    # Parse Java ALLOWED_FILES from source
    java_src = (ROOT / 'integration/java/src/org/allocation/integration/AllocationClient.java').read_text(encoding='utf-8')
    import re
    # extract the quoted filenames inside Arrays.asList(...)
    m = re.search(r'Arrays\.asList\((.*?)\)', java_src, re.S)
    body = m.group(1)
    java_files = set(re.findall(r'"([^"]+)"', body))
    py_files = set(mod.ALLOWED_FILES)
    check('Java/Python whitelist parity', java_files == py_files,
          'java=%s py=%s' % (sorted(java_files), sorted(py_files)))
    check('whitelist has 10 files', len(py_files) == 10, len(py_files))
    return py_files


def test_whitelist_matches_frozen_service():
    # The frozen service allowed map should be a superset/equal set.
    # We can't easily parse Go here, so assert the 10 known names are present.
    expected = {
        'results.csv', 'ledger.csv', 'ledger.csv.gz', 'reconciliation.csv',
        'source_to_pool.csv', 'source_to_pool.csv.gz', 'lineage_links.csv',
        'lineage_links.csv.gz', 'evidence_stream_manifest.json', 'run_summary.json'}
    check('whitelist == documented 10 names', set(mod.ALLOWED_FILES) == expected)


def test_capacity_preflight_edge():
    TOOL = ROOT / 'integration/tools/capacity_preflight.py'
    # huge pools -> BLOCK, return 2
    r = subprocess.run([sys.executable, str(TOOL), '--pools', '3600',
                        '--targets-per-pool', '500000', '--disk-path', tempfile.gettempdir()],
                       capture_output=True, text=True)
    check('preflight BLOCK huge', r.returncode == 2 and 'BLOCK' in r.stdout, r.stdout[:100])
    # small -> PASS
    r2 = subprocess.run([sys.executable, str(TOOL), '--pools', '3',
                         '--targets-per-pool', '4', '--reserve-free-gib', '0.001',
                         '--disk-path', tempfile.gettempdir()],
                        capture_output=True, text=True)
    check('preflight PASS small', r2.returncode == 0 and 'PASS' in r2.stdout, r2.stdout[:100])
    # boundary: overhead_factor < 1 must be rejected
    r3 = subprocess.run([sys.executable, str(TOOL), '--pools', '1', '--targets-per-pool', '1',
                         '--overhead-factor', '0.5', '--disk-path', tempfile.gettempdir()],
                        capture_output=True, text=True)
    check('preflight reject overhead<1', r3.returncode == 2, r3.returncode)
    # boundary: Decimal precision (huge but finite numbers)
    r4 = subprocess.run([sys.executable, str(TOOL), '--pools', '999999999999',
                         '--targets-per-pool', '999999999999', '--disk-path', tempfile.gettempdir()],
                        capture_output=True, text=True)
    check('preflight handles 10^12 scale without crash', r4.returncode in (0, 2), r4.returncode)


def test_capture_replay_path_traversal():
    CAP = ROOT / 'integration/tools/capture_replay_inputs.py'
    rule = ROOT / 'core/examples/manufacturing/rule.json'
    with tempfile.TemporaryDirectory() as d:
        snap = Path(d) / 'snap'
        # capture ok
        r = subprocess.run([sys.executable, str(CAP), '--root', str(rule.parent),
                            '--rule', str(rule), '--output', str(snap)],
                           capture_output=True, text=True)
        check('capture ok', r.returncode == 0, r.stderr)
        # verify ok
        r = subprocess.run([sys.executable, str(CAP), '--verify', str(snap)],
                           capture_output=True, text=True)
        check('verify ok', r.returncode == 0, r.stderr)
        # tamper -> reject
        (snap / 'files' / 'sales.csv').write_bytes(b'x')
        r = subprocess.run([sys.executable, str(CAP), '--verify', str(snap)],
                           capture_output=True, text=True)
        check('verify rejects tamper', r.returncode == 2, r.returncode)
        # destination already exists -> refuse overwrite
        r = subprocess.run([sys.executable, str(CAP), '--root', str(rule.parent),
                            '--rule', str(rule), '--output', str(snap)],
                           capture_output=True, text=True)
        check('capture refuses existing dest', r.returncode == 2, r.returncode)
        # destination inside root -> refuse
        r = subprocess.run([sys.executable, str(CAP), '--root', str(rule.parent),
                            '--rule', str(rule), '--output', str(rule.parent / 'inside_snap')],
                           capture_output=True, text=True)
        check('capture refuses dest inside root', r.returncode == 2, r.returncode)


if __name__ == '__main__':
    test_no_content_length_behavior()
    test_python_vs_java_whitelist_parity()
    test_whitelist_matches_frozen_service()
    test_capacity_preflight_edge()
    test_capture_replay_path_traversal()
    print('\n==== RESULT: %d passed, %d failed ====' % (PASS, FAIL))
    sys.exit(1 if FAIL else 0)
