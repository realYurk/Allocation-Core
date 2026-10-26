#!/usr/bin/env python3
import hashlib, pathlib, sys, zipfile
root=pathlib.Path(sys.argv[1]).resolve();out=pathlib.Path(sys.argv[2]).resolve();out.parent.mkdir(parents=True,exist_ok=True)
files=sorted([p for p in root.rglob('*') if p.is_file() and '.git' not in p.relative_to(root).parts and 'dist' not in p.relative_to(root).parts and p.name!='RELEASE_SHA256.txt' and '__pycache__' not in p.parts and p.suffix not in ('.pyc',)])
lines=[hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.relative_to(root).as_posix() for p in files]
(root/'RELEASE_SHA256.txt').write_text('\n'.join(lines)+'\n')
with zipfile.ZipFile(out,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=6) as z:
 for p in sorted(files+[root/'RELEASE_SHA256.txt']):z.write(p,arcname='Allocation-Core/'+p.relative_to(root).as_posix())
print(out)
