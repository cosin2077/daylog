#!/usr/bin/env python3
"""Build a standalone x64 runtime and a bounded, publisher-signed ZIP."""
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import zipfile

root=Path(__file__).resolve().parents[1]
version=os.environ.get('DAYLOG_VERSION','0.7.0')
if not re.fullmatch(r'\d+\.\d+\.\d+',version):raise SystemExit('Invalid numeric version')
development=os.environ.get('DAYLOG_DEVELOPMENT_BUILD')=='1'
key=os.environ.get('DAYLOG_RELEASE_KEY','')
if not key and not development:raise SystemExit('Release requires the fixed private signing identity')
out=root/'dist/windows-amd64';out.mkdir(parents=True,exist_ok=True)
env=os.environ.copy();env.update(GOOS='windows',GOARCH='amd64',CGO_ENABLED='0')
flags=f'-s -w -X main.version={version}'
if development and not key:flags+=' -X daylog/internal/update.developmentBuild=true'
subprocess.run(['go','build','-trimpath','-ldflags',flags,'-o',str(out/'daylog.exe'),'./cmd/daylog'],cwd=root,env=env,check=True)
digest=hashlib.sha256((out/'daylog.exe').read_bytes()).hexdigest()
message=f'daylog-windows-v1\n{version}\nwindows-amd64\n{digest}\n'.encode()
signature='development'
if key:
    # Never print the key, decrypted P12, password or raw command environment.
    p=subprocess.run(['openssl','dgst','-sha256','-sign',key],input=message,capture_output=True)
    if p.returncode:raise SystemExit('Windows artifact signing failed')
    signature=base64.b64encode(p.stdout).decode()
    with tempfile.TemporaryDirectory(prefix='daylog-publisher-') as directory:
        public=Path(directory)/'public.pem'
        p=subprocess.run(['openssl','x509','-inform','DER','-in',str(root/'internal/update/signing-certificate.der'),'-pubkey','-noout'],capture_output=True,check=True)
        public.write_bytes(p.stdout)
        sig=Path(directory)/'signature';sig.write_bytes(base64.b64decode(signature))
        verified=subprocess.run(['openssl','dgst','-sha256','-verify',str(public),'-signature',str(sig)],input=message,capture_output=True)
        if verified.returncode:raise SystemExit('Windows signing identity differs from the pinned publisher')
(out/'signature.json').write_text(json.dumps({'version':version,'platform':'windows-amd64','sha256':digest,'signature':signature},indent=2)+'\n')
assets=root/'skills/daylog/assets';assets.mkdir(parents=True,exist_ok=True)
package=assets/'daylog-windows-amd64.zip'
with zipfile.ZipFile(package,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=6) as archive:
    for name in ['daylog.exe','signature.json']:archive.write(out/name,name)
manifest=assets/'release.json'
feed=json.loads(manifest.read_text()) if manifest.exists() else {'version':version,'artifacts':{}}
if feed['version']!=version:feed={'version':version,'artifacts':{}}
feed['artifacts']['windows-amd64']={'url':package.name,'sha256':hashlib.sha256(package.read_bytes()).hexdigest()}
manifest.write_text(json.dumps(feed,indent=2)+'\n')
print(f'Windows amd64 {version} built; package signature: '+('fixed publisher' if key else 'development only'))
