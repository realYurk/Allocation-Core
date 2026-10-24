#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Capture immutable, independently verifiable COMPLETE INPUTS for a full run.

This supplements, but does not change, the frozen V20 Vault (results archive).
Snapshot layout: DEST/files/<root-relative paths> + DEST/inputs_manifest.json.
"""
import argparse
import hashlib
import json
import os
import shutil
import sys
import tempfile
from pathlib import Path


def sha256(path):
    h = hashlib.sha256()
    with path.open('rb') as f:
        for block in iter(lambda: f.read(1024 * 1024), b''):
            h.update(block)
    return h.hexdigest()


def confined(path, root):
    path = path.resolve(strict=True)
    try:
        rel = path.relative_to(root)
    except ValueError as e:
        raise ValueError('input outside trusted --root: '+str(path)) from e
    if not path.is_file():
        raise ValueError('not a regular input file: '+str(path))
    return rel.as_posix()


def capture(rule, root, output):
    root = root.resolve(strict=True)
    rule = rule.resolve(strict=True)
    rule_rel = confined(rule, root)
    output = output.resolve()
    if output.exists():
        raise ValueError('destination exists (refuse overwrite): '+str(output))
    if output == root or root in output.parents:
        raise ValueError('destination must be outside input --root')
    rule_spec = json.loads(rule.read_text(encoding='utf-8-sig'))
    datasets = rule_spec['datasets']
    paths = [rule]
    for group in ('amounts','drivers'):
        for rel in datasets[group].values():
            if Path(rel).is_absolute():
                raise ValueError('dataset path must be relative: '+rel)
            paths.append((rule.parent / rel).resolve(strict=True))
    files = {confined(src,root):src for src in paths}
    output.parent.mkdir(parents=True, exist_ok=True)
    staged = Path(tempfile.mkdtemp(prefix='.allocation-input-snapshot-', dir=output.parent))
    try:
        records = []
        for rel, src in sorted(files.items()):
            target = staged / 'files' / rel
            target.parent.mkdir(parents=True, exist_ok=True)
            hash_before = sha256(src)
            shutil.copyfile(src, target)
            hash_after = sha256(src)
            hash_copied = sha256(target)
            if hash_before != hash_after or hash_before != hash_copied:
                raise IOError('input mutated while snapshot captured: '+str(src))
            records.append({'path':rel,'bytes':target.stat().st_size,'sha256':hash_copied})
        manifest = {'contract':'allocation-input-snapshot/1.0',
                    'important':'Complete raw INPUTS and rule only. This is distinct from the frozen V20 Vault output archive.',
                    'rule_under_files':rule_rel,
                    'files':records}
        (staged/'inputs_manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
        staged.rename(output)
        return manifest
    finally:
        if staged.exists(): shutil.rmtree(staged)


def verify(snapshot):
    snapshot = snapshot.resolve(strict=True)
    manifest = json.loads((snapshot/'inputs_manifest.json').read_text('utf-8'))
    if manifest['contract'] != 'allocation-input-snapshot/1.0':
        raise ValueError('unsupported snapshot contract')
    for item in manifest['files']:
        p = (snapshot/'files'/item['path']).resolve(strict=True)
        try:
            p.relative_to((snapshot/'files').resolve())
        except ValueError as e:
            raise ValueError('snapshot contains escaping path') from e
        if p.stat().st_size != item['bytes'] or sha256(p) != item['sha256']:
            raise ValueError('snapshot altered: '+item['path'])
    return len(manifest['files'])


def main():
    p = argparse.ArgumentParser(description='Capture or verify full-recalculation input files; not a Vault result archive.')
    p.add_argument('--rule', type=Path, help='Input rule.json path')
    p.add_argument('--root', type=Path, help='Trusted parent for rule and datasets')
    p.add_argument('--output', type=Path, help='New, unique snapshot output directory')
    p.add_argument('--verify', type=Path, help='Verify an existing snapshot instead')
    args = p.parse_args()
    try:
        if args.verify:
            count = verify(args.verify)
            print('PASS: %d complete input files match snapshot hashes' % count)
        else:
            if not (args.rule and args.root and args.output): p.error('capture requires --rule, --root and --output')
            report = capture(args.rule,args.root,args.output)
            print('PASS: %d input files captured; replay rule: %s' % (len(report['files']), args.output/'files'/report['rule_under_files']))
    except (OSError,ValueError,KeyError) as e:
        print('FAIL: '+str(e),file=sys.stderr)
        return 2
    return 0

if __name__ == '__main__':
    sys.exit(main())
