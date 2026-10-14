#!/usr/bin/env python3
import hashlib
from pathlib import Path
repo=Path(__file__).resolve().parents[1]
cert=(repo/'internal/update/signing-certificate.der').read_bytes()
installer=(repo/'skills/daylog/scripts/install-runtime.sh').read_text()
for name, value in [('sha1',hashlib.sha1(cert).hexdigest()),('sha256',hashlib.sha256(cert).hexdigest())]:
 if "daylog_signer_"+name+"='"+value+"'" not in installer:raise SystemExit('Installer publisher pin differs from embedded update certificate')
print('Installer and updater publisher pins agree')
