#!/usr/bin/env python3
"""Create a persistent, encrypted self-signed code-signing identity (once)."""
import argparse
import os
from pathlib import Path
import secrets
import subprocess

parser=argparse.ArgumentParser()
parser.add_argument('--directory',required=True)
a=parser.parse_args()
root=Path(a.directory).expanduser().resolve()
if root.exists():parser.error('identity directory already exists; refusing to rotate or overwrite')
os.umask(0o077)
root.mkdir(parents=True,mode=0o700)
(root/'password').write_text(secrets.token_urlsafe(48))
(root/'openssl.cnf').write_text("""[req]
prompt = no
distinguished_name = dn
x509_extensions = signing
[dn]
CN = Daylog Release Signing
O = Daylog
[signing]
basicConstraints = critical,CA:FALSE
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
subjectKeyIdentifier = hash
""")
def run(args, secret_input=None):
 p=subprocess.run(args,input=secret_input,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
 if p.returncode:raise SystemExit('Identity generation failed; inspect the private directory before retrying')
run(['/usr/bin/openssl','req','-new','-x509','-newkey','rsa:3072','-sha256','-days','3650','-config',str(root/'openssl.cnf'),'-keyout',str(root/'private-key.pem'),'-out',str(root/'certificate.pem'),'-passout','file:'+str(root/'password')])
run(['/usr/bin/openssl','x509','-in',str(root/'certificate.pem'),'-outform','DER','-out',str(root/'certificate.der')])
run(['/usr/bin/openssl','pkcs12','-export','-inkey',str(root/'private-key.pem'),'-passin','file:'+str(root/'password'),'-in',str(root/'certificate.pem'),'-out',str(root/'identity.p12'),'-passout','stdin'], (root/'password').read_bytes()+b'\n')
print('Encrypted signing identity created in the requested private directory. Back up identity.p12 and password securely; never commit them.')
