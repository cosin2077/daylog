#!/usr/bin/env python3
"""Verify integrity, bundle identifier and exact pinned signing certificate."""
import hashlib
from pathlib import Path
import subprocess
import sys
import tempfile

repo=Path(__file__).resolve().parents[1]
cert=(repo/'internal/update/signing-certificate.der').read_bytes()
app=Path(sys.argv[1])
requirement='identifier "io.daylog.agent" and certificate leaf = H"'+hashlib.sha1(cert).hexdigest()+'"'
subprocess.run(['/usr/bin/codesign','--verify','--deep','--strict','-R','='+requirement,str(app)],check=True)
with tempfile.TemporaryDirectory(prefix='daylog-signer-check-') as directory:
 prefix=str(Path(directory)/'cert')
 subprocess.run(['/usr/bin/codesign','-d','--extract-certificates='+prefix,str(app)],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
 if (Path(prefix+'0')).read_bytes()!=cert:raise SystemExit('Signing certificate does not match the pinned publisher')
print('Pinned Daylog signing identity verified')
