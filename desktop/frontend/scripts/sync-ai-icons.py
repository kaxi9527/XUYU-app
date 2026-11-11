"""Verified, versioned monochrome brand assets; no JS package installation."""
import base64
import hashlib
import io
import json
import re
import tarfile
import urllib.request
from pathlib import Path

version='1.102.0'
url=f'https://registry.npmjs.org/@lobehub/icons-static-svg/-/icons-static-svg-{version}.tgz'
integrity='3Na6S2kdslmsfnBjuL0PQWQClrmuWe/pWThRN/3J2VzPg5MhVghy8knVu7kd1/lqA9ydZBvwmnKLzuBo0Wm6Qw=='
cache=Path.home()/f'Library/Caches/WorkShelf/upstream-assets/lobe-icons/{version}/package.tgz'
if cache.exists(): data=cache.read_bytes()
else:
    data=urllib.request.urlopen(url,timeout=45).read(8_000_001)
    if len(data)>8_000_000: raise SystemExit('Asset archive too large')
    if base64.b64encode(hashlib.sha512(data).digest()).decode()!=integrity: raise SystemExit('Integrity mismatch')
    cache.parent.mkdir(parents=True,exist_ok=True);cache.write_bytes(data)
if base64.b64encode(hashlib.sha512(data).digest()).decode()!=integrity: raise SystemExit('Cached integrity mismatch')
frontend=Path(__file__).resolve().parents[1]
target=frontend/f'public/vendor/lobe-icons-{version}'
target.mkdir(parents=True,exist_ok=True)
manifest={'name':'@lobehub/icons-static-svg','version':version,'license':'MIT','url':url,'integrity':'sha512-'+integrity,'files':{}}
names=[]
with tarfile.open(fileobj=io.BytesIO(data),mode='r:gz') as archive:
    for member in archive.getmembers():
        if not member.isfile(): continue
        match=re.fullmatch(r'package/icons/([a-z0-9]+)\.svg',member.name)
        if not match and member.name not in ['package/LICENSE','package/LICENSE.md','package/package.json']: continue
        content=archive.extractfile(member).read()
        if match:
            # Preserve upstream geometry; reject active/external SVG content.
            if re.search(rb'<script|<foreignObject|\bon\w+\s*=|(?:href|src)\s*=',content,re.I): raise SystemExit('Unexpected active SVG: '+member.name)
            names.append(match[1])
        filename=Path(member.name).name
        (target/filename).write_bytes(content)
        manifest['files'][filename]=hashlib.sha256(content).hexdigest()
(target/'sources.json').write_text(json.dumps(manifest,indent=2)+'\n')
(frontend/'src/ai-icon-catalog.json').write_text(json.dumps({'version':version,'icons':sorted(names)},indent=2)+'\n')
print(json.dumps({'count':len(names),'bytes':sum((target/f).stat().st_size for f in manifest['files']),'popular':{x:x in names for x in ['codex','openai','claudecode','claude','workbuddy','antigravity','deepseek','qwen','qoder','opencode','grok','doubao','trae','cursor']}}))
