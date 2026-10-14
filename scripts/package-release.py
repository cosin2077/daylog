#!/usr/bin/env python3
"""Prepare GitHub assets from a complete, version-checked local build."""
import argparse
import hashlib
import json
from pathlib import Path
import plistlib
import re
import shutil
import tarfile
import tempfile
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--version', required=True)
parser.add_argument('--repository', default='cosin2077/daylog')
args = parser.parse_args()
if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+', args.version):
    parser.error('version must be numeric major.minor.patch')
if not re.fullmatch(r'[\w.-]+/[\w.-]+', args.repository):
    parser.error('repository must be owner/name')
root = Path(__file__).resolve().parents[1]
assets = root / 'skills/daylog/assets'
feed = json.loads((assets / 'release.json').read_text())
if feed['version'] != args.version or set(feed['artifacts']) != {'darwin-arm64', 'darwin-amd64'}:
    parser.error('build both architectures at the requested version first')

for arch in ('arm64', 'amd64'):
    name = f'daylog-macos-{arch}.tar.gz'
    blob = (assets / name).read_bytes()
    if hashlib.sha256(blob).hexdigest() != feed['artifacts'][f'darwin-{arch}']['sha256']:
        parser.error(f'checksum mismatch: {name}')
    with tarfile.open(assets / name) as package:
        info = plistlib.loads(package.extractfile('Daylog.app/Contents/Info.plist').read())
        if info['CFBundleShortVersionString'] != args.version or info['CFBundleIdentifier'] != 'io.daylog.agent':
            parser.error(f'bundle version/identity mismatch: {name}')

    # Public releases must never contain a development/ad-hoc package.
    with tempfile.TemporaryDirectory(prefix='daylog-release-signature-') as directory:
        with tarfile.open(assets / name) as package:
            package.extractall(directory, filter='data')
        subprocess.run(['python3', str(root/'scripts/verify-signer.py'), str(Path(directory)/'Daylog.app')], check=True)

output = root / 'dist/release'
if output.exists():
    shutil.rmtree(output)
output.mkdir(parents=True)
for arch in ('arm64', 'amd64'):
    name = f'daylog-macos-{arch}.tar.gz'
    shutil.copyfile(assets / name, output / name)
    # Keep latest discovery separate from immutable version-specific downloads.
    feed['artifacts'][f'darwin-{arch}']['url'] = f'https://github.com/{args.repository}/releases/download/v{args.version}/{name}'
(output / 'release.json').write_text(json.dumps(feed, indent=2) + '\n')

# The public skill stays small; its installer downloads only the user's arch.
with tarfile.open(output / 'daylog-skill.tar.gz', 'w:gz') as package:
    skill = root / 'skills/daylog'
    for file in sorted(skill.rglob('*')):
        relative = file.relative_to(skill)
        if file.is_file() and not file.is_symlink() and relative.parts[0] != 'assets' and file.name != '.DS_Store':
            package.add(file, arcname=str(Path('daylog') / relative), recursive=False)
checksums = []
for file in sorted(output.iterdir()):
    checksums.append(f'{hashlib.sha256(file.read_bytes()).hexdigest()}  {file.name}')
(output / 'checksums.txt').write_text('\n'.join(checksums) + '\n')
print(f'Release {args.version} prepared in {output}')
