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
import zipfile
import base64

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
if feed['version'] != args.version or set(feed['artifacts']) != {'darwin-arm64', 'darwin-amd64', 'windows-amd64'}:
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

# Authenticate the Windows executable with the same pinned RSA publisher key.
windows_package=assets/'daylog-windows-amd64.zip'
if hashlib.sha256(windows_package.read_bytes()).hexdigest()!=feed['artifacts']['windows-amd64']['sha256']:parser.error('Windows checksum mismatch')
with zipfile.ZipFile(windows_package) as package:
    if sorted(package.namelist())!=['daylog.exe','signature.json']:parser.error('Unexpected Windows package contents')
    metadata=json.loads(package.read('signature.json'))
    digest=hashlib.sha256(package.read('daylog.exe')).hexdigest()
    if metadata['version']!=args.version or metadata['platform']!='windows-amd64' or metadata['sha256']!=digest:parser.error('Windows package metadata mismatch')
    with tempfile.TemporaryDirectory(prefix='daylog-windows-verify-') as directory:
        public=Path(directory)/'public.pem';signature=Path(directory)/'signature'
        pub=subprocess.run(['openssl','x509','-inform','DER','-in',str(root/'internal/update/signing-certificate.der'),'-pubkey','-noout'],capture_output=True,check=True)
        public.write_bytes(pub.stdout);signature.write_bytes(base64.b64decode(metadata['signature'],validate=True))
        message=f'daylog-windows-v1\n{args.version}\nwindows-amd64\n{digest}\n'.encode()
        result=subprocess.run(['openssl','dgst','-sha256','-verify',str(public),'-signature',str(signature)],input=message,capture_output=True)
        if result.returncode:parser.error('Windows publisher signature mismatch')

output = root / 'dist/release'
if output.exists():
    shutil.rmtree(output)
output.mkdir(parents=True)
for arch in ('arm64', 'amd64'):
    name = f'daylog-macos-{arch}.tar.gz'
    shutil.copyfile(assets / name, output / name)
    # Keep latest discovery separate from immutable version-specific downloads.
    feed['artifacts'][f'darwin-{arch}']['url'] = f'https://github.com/{args.repository}/releases/download/v{args.version}/{name}'
shutil.copyfile(windows_package, output/windows_package.name)
feed['artifacts']['windows-amd64']['url']=f'https://github.com/{args.repository}/releases/download/v{args.version}/{windows_package.name}'
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
