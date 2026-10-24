#!/usr/bin/env python3
"""Verify one immutable *build* manifest. Runtime has no product version string."""
from pathlib import Path
import hashlib
import json
import sys
ROOT = Path(__file__).resolve().parents[2]
def main():
    info=json.loads((ROOT/'RELEASE_IDENTITY.json').read_text('utf-8'))
    hashes=json.loads((ROOT/'CORE_SHA256.json').read_text('utf-8'))
    present={p.relative_to(ROOT/'core').as_posix():p for p in (ROOT/'core').rglob('*') if p.is_file() and '__pycache__' not in p.parts}
    bad=[rel for rel,expected in hashes.items() if rel not in present or hashlib.sha256(present[rel].read_bytes()).hexdigest()!=expected]
    extra=sorted(set(present)-set(hashes))
    report={'product':info['product'],'build_id':info['build_id'], 'integrity':'PASS' if not bad and not extra else 'FAIL','tracked_files':len(hashes),'mismatch':bad[:10],'unexpected_files':extra[:10]}
    print(json.dumps(report,ensure_ascii=False,indent=2))
    return 0 if report['integrity']=='PASS' else 1
if __name__=='__main__':sys.exit(main())
