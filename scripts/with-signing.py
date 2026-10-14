#!/usr/bin/env python3
"""Run a build with an ephemeral keychain; remove only task-owned signing trust and keychain search entries.

Input: --identity-dir (maintainer) or DAYLOG_SIGNING_P12_BASE64 and
DAYLOG_SIGNING_PASSWORD (CI secrets). The public certificate is pinned in repo.
"""
import argparse
import base64
import hashlib
import os
from pathlib import Path
import secrets
import plistlib
import shlex
import signal
import subprocess
import tempfile

parser=argparse.ArgumentParser()
parser.add_argument('--identity-dir')
parser.add_argument('command',nargs=argparse.REMAINDER)
a=parser.parse_args();command=a.command
if command[:1]==['--']:command=command[1:]
if not command:parser.error('provide a command after --')
repo=Path(__file__).resolve().parents[1]
cert=(repo/'internal/update/signing-certificate.der').read_bytes()
identity=hashlib.sha1(cert).hexdigest().upper()
os.umask(0o077)
child=None
def interrupted(signum, _frame):
 if child is not None and child.poll() is None:
  os.killpg(child.pid,signal.SIGTERM)
  try:child.wait(timeout=10)
  except subprocess.TimeoutExpired:os.killpg(child.pid,signal.SIGKILL);child.wait()
 raise SystemExit(128+signum)
for sig in (signal.SIGTERM,signal.SIGINT,signal.SIGHUP):signal.signal(sig,interrupted)
def run(args,description):
 print(description,flush=True)
 try:p=subprocess.run(args,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE,timeout=90 if os.environ.get('GITHUB_ACTIONS')=='true' else 300)
 except subprocess.TimeoutExpired:raise SystemExit(description+' timed out (private arguments suppressed)') from None
 if p.returncode:
  detail=p.stderr.decode(errors='replace')
  for secret in (globals().get('pw',''),globals().get('password',''),os.environ.get('DAYLOG_SIGNING_P12_BASE64','')):
   if secret:detail=detail.replace(secret,'[REDACTED]')
  raise SystemExit(description+' failed: '+detail[-800:])
with tempfile.TemporaryDirectory(prefix='daylog-signing-') as directory:
 temp=Path(directory);keychain=temp/'build.keychain-db';pw=secrets.token_urlsafe(40)
 if a.identity_dir:
  root=Path(a.identity_dir).expanduser();p12=(root/'identity.p12').read_bytes();password=(root/'password').read_text().strip()
 else:
  encoded=os.environ.get('DAYLOG_SIGNING_P12_BASE64','');password=os.environ.get('DAYLOG_SIGNING_PASSWORD','')
  if not encoded or not password:raise SystemExit('Release signing secrets are required; ad-hoc fallback is forbidden')
  try:p12=base64.b64decode(encoded,validate=True)
  except ValueError:raise SystemExit('Invalid signing secret encoding')
 package=temp/'identity.p12';package.write_bytes(p12)
 run(['/usr/bin/security','create-keychain','-p',pw,str(keychain)],'Create temporary keychain')
 trust_added=False
 search_added=False
 ci_certificate_added=False
 trust_domain=['-d'] if os.environ.get('GITHUB_ACTIONS')=='true' else []
 trust_prefix=['/usr/bin/sudo','-n'] if trust_domain else []
 disposable_ci=bool(trust_domain) and os.environ.get('RUNNER_ENVIRONMENT')=='github-hosted'
 def search_paths():
  p=subprocess.run(['/usr/bin/security','list-keychains','-d','user'],capture_output=True,text=True)
  if p.returncode:raise SystemExit('Cannot read keychain search list')
  return shlex.split(p.stdout)
 try:
  run(['/usr/bin/security','unlock-keychain','-p',pw,str(keychain)],'Unlock temporary keychain')
  run(['/usr/bin/security','set-keychain-settings','-lut','21600',str(keychain)],'Configure temporary keychain')
  trust_file=temp/'trust.plist'
  exported=subprocess.run(trust_prefix+['/usr/bin/security','trust-settings-export',*trust_domain,str(trust_file)],stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
  had_trust=False
  if exported.returncode and not any(t in exported.stderr.lower() for t in (b'could not be found',b'no trust settings',b'not found')):
   raise SystemExit('Cannot inspect prior signing trust; refusing to alter it')
  if exported.returncode==0:
   trust_data=subprocess.run(trust_prefix+['/bin/cat',str(trust_file)],stdout=subprocess.PIPE,stderr=subprocess.PIPE)
   if trust_data.returncode:raise SystemExit('Cannot read exported signing trust; refusing to alter it')
   had_trust=identity in {str(k).upper() for k in plistlib.loads(trust_data.stdout).get('trustList',{})}
  run(['/usr/bin/security','list-keychains','-d','user','-s',str(keychain),*search_paths()],'Add temporary keychain search entry')
  search_added=True
  if not had_trust:
   # In CI, privileged trust settings use the system PUBLIC certificate store;
   # the signing private key remains in the runner user's temporary keychain.
   trust_keychain='/Library/Keychains/System.keychain' if trust_domain else str(keychain)
   if trust_domain:
    present=subprocess.run(trust_prefix+['/usr/bin/security','find-certificate','-a','-Z','-c','Daylog Release Signing',trust_keychain],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=30)
    ci_certificate_added=identity.encode() not in present.stdout.upper()
   run(trust_prefix+['/usr/bin/security','add-trusted-cert',*trust_domain,'-r','trustRoot','-p','codeSign','-k',trust_keychain,str(repo/'internal/update/signing-certificate.der')],'Temporarily trust build signing certificate for code signing')
   trust_added=True
  run(['/usr/bin/security','unlock-keychain','-p',pw,str(keychain)],'Unlock temporary keychain after authorization')
  run(['/usr/bin/security','import',str(package),'-k',str(keychain),'-P',password,'-T','/usr/bin/codesign'],'Import signing identity')
  run(['/usr/bin/security','set-key-partition-list','-S','apple-tool:,apple:,codesign:','-s','-k',pw,str(keychain)],'Set signing key access')
  env=os.environ.copy()
  env.pop('DAYLOG_SIGNING_P12_BASE64',None);env.pop('DAYLOG_SIGNING_PASSWORD',None)
  env['DAYLOG_SIGNING_IDENTITY']=identity;env['DAYLOG_SIGNING_KEYCHAIN']=str(keychain);env['DAYLOG_REQUIRE_SIGNING']='1'
  child=subprocess.Popen(command,cwd=repo,env=env,start_new_session=True)
  result=child.wait()
 finally:
  cleanup_errors=[]
  def cleanup(args,description):
   try:
    p=subprocess.run(args,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=30)
    if p.returncode:cleanup_errors.append(description)
   except subprocess.TimeoutExpired:cleanup_errors.append(description)
  if trust_added:
   if disposable_ci:
    # Some macOS hosted images prompt when removing admin trust even as root.
    # This public, codeSign-only trust is discarded with the isolated VM.
    # Private signing material is still removed below, before later steps.
    print('Public code-signing trust is confined to this disposable GitHub-hosted runner; VM disposal removes it.',flush=True)
   else:
    cleanup(trust_prefix+['/usr/bin/security','remove-trusted-cert',*trust_domain,str(repo/'internal/update/signing-certificate.der')],'temporary signing trust')
  if ci_certificate_added:
   cleanup(trust_prefix+['/usr/bin/security','delete-certificate','-Z',identity,'/Library/Keychains/System.keychain'],'temporary CI public signing certificate')
  if search_added:
   try:
    keep=[p for p in search_paths() if os.path.realpath(p)!=os.path.realpath(keychain)]
    cleanup(['/usr/bin/security','list-keychains','-d','user','-s',*keep],'temporary keychain search entry')
   except SystemExit:cleanup_errors.append('keychain search list')
  cleanup(['/usr/bin/security','delete-keychain',str(keychain)],'temporary keychain')
  if cleanup_errors:raise SystemExit('Signing cleanup incomplete: '+', '.join(cleanup_errors))
 if result:raise SystemExit(result)
