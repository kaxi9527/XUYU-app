"""Check release inputs without printing the contents of a possible secret."""
import re
import sys
from pathlib import Path

root = Path(sys.argv[1])
patterns = {
    "private key": re.compile(rb"-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----"),
    "GitHub token": re.compile(rb"\b(?:gh[opusr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{40,})\b"),
    "AWS access key": re.compile(rb"\b(?:AKIA|ASIA)[A-Z0-9]{16}\b"),
    "Google API key": re.compile(rb"\bAIza[0-9A-Za-z_-]{35}\b"),
    "Slack token": re.compile(rb"\bxox[baprs]-[A-Za-z0-9-]{20,}\b"),
}
failures = []
for path in root.rglob("*"):
    if not path.is_file():
        continue
    name = path.name.lower()
    if name == ".workshelf-origin.jsonl" or name == ".env" or name.endswith((".sqlite", ".sqlite3", ".jsonl")):
        failures.append((path, "private runtime data file"))
    data = path.read_bytes()
    for label, pattern in patterns.items():
        if pattern.search(data):
            failures.append((path, label))

for path, label in failures:
    print(f"{path.relative_to(root)}: {label}")
if failures:
    raise SystemExit(1)
print("Release contents: no matching private data files or known credential formats")
