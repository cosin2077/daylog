#!/usr/bin/env python3
"""Sign an app with the pinned maintainer certificate, or explicitly for development."""
import hashlib
import os
from pathlib import Path
import subprocess
import sys

repo=Path(__file__).resolve().parents[1]
app=Path(sys.argv[1])
cert=(repo/'internal/update/signing-certificate.der').read_bytes()
fingerprint=hashlib.sha1(cert).hexdigest().upper()
identity=os.environ.get('DAYLOG_SIGNING_IDENTITY','')
if not identity:
 if os.environ.get('DAYLOG_REQUIRE_SIGNING')=='1' or os.environ.get('DAYLOG_DEVELOPMENT_BUILD')!='1':
  raise SystemExit('Fixed signing identity required. Development only: DAYLOG_DEVELOPMENT_BUILD=1')
 subprocess.run(['/usr/bin/codesign','--force','--sign','-',str(app)],check=True)
else:
 if identity.upper()!=fingerprint:raise SystemExit('Signing identity does not match the repository certificate')
 keychain=os.environ.get('DAYLOG_SIGNING_KEYCHAIN','')
 if not keychain:raise SystemExit('Explicit temporary signing keychain required')
 requirement='designated => identifier "io.daylog.agent" and certificate leaf = H"'+fingerprint+'"'
 subprocess.run(['/usr/bin/codesign','--force','--sign',identity,'--keychain',keychain,'--timestamp=none','--requirements','='+requirement,str(app)],check=True)
 subprocess.run([sys.executable,str(repo/'scripts/verify-signer.py'),str(app)],check=True)
subprocess.run(['/usr/bin/codesign','--verify','--deep','--strict',str(app)],check=True)
