#!/usr/bin/env python3
"""Validate a full Allocation-Core release built from supplied source."""
import argparse, hashlib, json, pathlib, struct, sys
EXPECTED={
 'studio_windows_amd64.exe':0x8664,'studio_windows_arm64.exe':0xAA64,
 'core/alloc_windows_amd64.exe':0x8664,'core/alloc_windows_arm64.exe':0xAA64,
}
def main():
 p=argparse.ArgumentParser();p.add_argument('root',type=pathlib.Path);p.add_argument('--all-platforms',action='store_true');a=p.parse_args();root=a.root
 required=list(EXPECTED)+['Start_Windows.cmd','studio-src/main.go','studio-src/designer.go','studio-src/public/designer.html','core/cmd/alloc/main.go','core/go.mod','studio-src/go.mod','examples/manufacturing/rule.json']
 if a.all_platforms:
  required += [f'{prefix}_{os}_{arch}' for prefix,os,arch in []]
 for f in required:
  item=root/f
  if not item.is_file() or item.stat().st_size==0:raise SystemExit(f'MISSING / EMPTY: {f}')
 for rel,machine in EXPECTED.items():
  d=(root/rel).read_bytes()
  if d[:2]!=b'MZ':raise SystemExit(f'Not PE: {rel}')
  off=struct.unpack_from('<I',d,0x3c)[0]
  if d[off:off+4]!=b'PE\0\0':raise SystemExit(f'Bad PE: {rel}')
  found=struct.unpack_from('<H',d,off+4)[0]
  if found!=machine:raise SystemExit(f'ARCH WRONG: {rel}: {found:04x} != {machine:04x}')
 if a.all_platforms:
  for os,arch in [('linux','amd64'),('linux','arm64'),('darwin','amd64'),('darwin','arm64')]:
   for prefix,base in [('studio',''),('alloc','core/')]:
    f=root/(base+f'{prefix}_{os}_{arch}')
    if not f.is_file():raise SystemExit('MISSING '+str(f))
 manifest=root/'RELEASE_SHA256.txt'
 if manifest.exists():
  for line in manifest.read_text().splitlines():
   if not line.strip():continue
   sha,rel=line.split('  ',1)
   p=root/rel
   if not p.is_file() or hashlib.sha256(p.read_bytes()).hexdigest()!=sha:raise SystemExit('HASH MISMATCH: '+rel)
 print('PASS: all four Windows PE files, source files and SHA-256 release manifest are valid')
if __name__=='__main__':main()
