#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Report canonical *distribution* version and frozen component provenance."""
from pathlib import Path
import hashlib
import json
import sys

ROOT = Path(__file__).resolve().parents[2]

def main():
    info = json.loads((ROOT / 'RELEASE_IDENTITY.json').read_text('utf-8'))
    core_hashes = json.loads((ROOT / 'CORE_SHA256.json').read_text('utf-8'))
    errors = []
    for rel, expected in core_hashes.items():
        f = ROOT / 'core' / rel
        if not f.is_file() or hashlib.sha256(f.read_bytes()).hexdigest() != expected:
            errors.append(rel)
    print(json.dumps({'release': info, 'frozen_core_files': len(core_hashes),
                      'frozen_core_sha256': 'PASS' if not errors else 'FAIL',
                      'mismatches': errors[:20]}, indent=2, ensure_ascii=False))
    return 1 if errors else 0

if __name__ == '__main__':
    sys.exit(main())
