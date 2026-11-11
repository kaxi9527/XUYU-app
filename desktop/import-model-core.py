"""Import only the UXQA model-runtime dependency closure, never its app or state.

Run with the go-list JSON and the verified source module as arguments. This is
source integration, not an environment/baseline copy. No dependency download.
"""
import hashlib
import json
import sys
from pathlib import Path

source = Path(sys.argv[1]).resolve()
raw = Path(sys.argv[2]).read_text()
target = Path(__file__).resolve().parent / 'model-core'
if target.exists():
    raise SystemExit('Refusing to overwrite an existing imported module')
decoder = json.JSONDecoder()
paths = {Path(n) for n in ['go.mod', 'go.sum', 'LICENSE', 'NOTICE.md', 'UPSTREAM-MANIFEST.json']}
while raw.strip():
    raw = raw.lstrip()
    pkg, end = decoder.raw_decode(raw)
    raw = raw[end:]
    if not pkg.get('ImportPath', '').startswith('github.com/yetone/magpie/'):
        continue
    directory = Path(pkg['Dir']).resolve()
    if not directory.is_relative_to(source):
        raise SystemExit('Unexpected package source')
    paths.update(p.relative_to(source) for p in directory.glob('*.go'))
    for field in ['CFiles', 'CXXFiles', 'MFiles', 'HFiles', 'FFiles', 'SFiles', 'SysoFiles', 'EmbedFiles']:
        for name in pkg.get(field, []):
            path = directory / name
            if not path.resolve().is_relative_to(source):
                raise SystemExit('Embedded asset escapes source')
            paths.add(path.relative_to(source))
manifest = {'source': 'UXQA native/model-core', 'upstream': 'https://github.com/yetone/magpie', 'license': 'MIT', 'changes': 'Dedicated WORKSHELF_MODEL_HOME/TOKEN; own command name. Other source unchanged at import.', 'files': {}}
for rel in sorted(paths):
    data = (source / rel).read_bytes()
    dest_rel = Path(str(rel).replace('cmd/uxqa-core/', 'cmd/workshelf-core/'))
    copied = data
    if rel.suffix == '.go':
        copied = copied.replace(b'UXQA_MODEL_HOME', b'WORKSHELF_MODEL_HOME').replace(b'UXQA_MODEL_TOKEN', b'WORKSHELF_MODEL_TOKEN')
    dest = target / dest_rel
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_bytes(copied)
    manifest['files'][str(dest_rel)] = {'source': str(rel), 'upstreamSHA256': hashlib.sha256(data).hexdigest(), 'importSHA256': hashlib.sha256(copied).hexdigest()}
(target / 'WORKSHELF-IMPORT.json').write_text(json.dumps(manifest, indent=2) + '\n')
print(json.dumps({'files': len(paths), 'target': str(target), 'bytes': sum((target / p).stat().st_size for p in manifest['files'])}))
