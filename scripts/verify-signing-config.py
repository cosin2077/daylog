#!/usr/bin/env python3
import hashlib
import base64
from pathlib import Path
repo=Path(__file__).resolve().parents[1]
cert=(repo/'internal/update/signing-certificate.der').read_bytes()
installer=(repo/'skills/daylog/scripts/install-runtime.sh').read_text()
for name, value in [('sha1',hashlib.sha1(cert).hexdigest()),('sha256',hashlib.sha256(cert).hexdigest())]:
 if "daylog_signer_"+name+"='"+value+"'" not in installer:raise SystemExit('Installer publisher pin differs from embedded update certificate')
if base64.b64encode(cert).decode() not in (repo/'skills/daylog/scripts/install-runtime.ps1').read_text():raise SystemExit('Windows installer publisher pin differs')
print('Installers and updater publisher pins agree')
