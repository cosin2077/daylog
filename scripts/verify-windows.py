#!/usr/bin/env python3
"""Windows CI: real executable/installer/process lifecycle, synthetic images.

No provider calls, no real-user desktop claims, no global process termination.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import winreg
import zipfile

repo=Path(__file__).resolve().parents[1]
assets=repo/'skills/daylog/assets'
environment={**os.environ,'DAYLOG_DEVELOPMENT_INSTALL':'1'}
result={'windows_ci':True,'user_desktop_verified':False}
process=None
root=Path(tempfile.mkdtemp(prefix='daylog windows verify '))
data=root/'private data'
install=root/'installed bin'
exe=install/'daylog.exe'
with winreg.OpenKey(winreg.HKEY_CURRENT_USER,'Environment',0,winreg.KEY_READ) as key:
    try:old_path,old_path_kind=winreg.QueryValueEx(key,'Path')
    except FileNotFoundError:old_path,old_path_kind=None,None

def run(command,expected=0,env=None,timeout=60):
    p=subprocess.run([str(x) for x in command],cwd=repo,env=env or environment,capture_output=True,text=True,encoding='utf-8',errors='replace',timeout=timeout)
    if p.returncode!=expected:raise AssertionError(f'{Path(str(command[0])).name} returned {p.returncode}: {p.stderr[-2500:]} {p.stdout[-1500:]}')
    return p.stdout

def cli(*args,**kw):return run([exe,'--data',data,*args],**kw)

def wait_status(predicate,timeout=15):
    until=time.monotonic()+timeout
    while time.monotonic()<until:
        try:
            value=json.loads(cli('status','--json'))
            if predicate(value):return value
        except AssertionError:pass
        time.sleep(.1)
    raise AssertionError('Expected runtime status did not arrive')

def installer(target,source,expected=0,strict=False,shell='pwsh'):
    return run([shell,'-NoProfile','-NonInteractive','-File',repo/'skills/daylog/scripts/install-runtime.ps1','-Source',source,'-InstallDir',target],expected=expected,env={**environment,'DAYLOG_DEVELOPMENT_INSTALL':'0' if strict else '1'},timeout=90)

try:
    feed=assets/'release.json'
    installer(install,feed)
    version=json.loads(cli('version','--json'))['version']
    assert cli().startswith('Daylog '+version)
    # Exercise Windows PowerShell 5.1 as well as pwsh. No ExecutionPolicy bypass.
    installer(install,feed,shell='powershell.exe')
    cli('init')
    config=data/'config.json'
    c=json.loads(config.read_text(encoding='utf-8'));c['agent']['enabled']=False;c['ai']['enabled']=False;c['update']['source']=str(feed)
    config.write_text(json.dumps(c),encoding='utf-8');config_before=config.read_bytes()
    result['install_and_help']=version
    fixture=root/'synthetic.jpg';run(['go','run','./scripts/fixture',fixture])
    process=subprocess.Popen([str(exe),'--data',str(data),'run','--fixture',str(fixture)],cwd=repo,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    initial=wait_status(lambda s:s['saved_frames']>0)
    cli('run','--fixture',fixture,expected=1)
    cli('pause','--minutes','0');wait_status(lambda s:s['paused'])
    cli('resume');wait_status(lambda s:not s['paused'])
    cli('stop');process.wait(timeout=15)
    result['synthetic_capture_single_instance_pause_stop']=True
    batch=json.loads(cli('pending','--json'))[0]['batch']
    report=root/'interpretation.json';report.write_text(json.dumps({'batch_id':batch['id'],'segments':[{'title':'Synthetic activity','summary':'Synthetic fixture only.','category':'写代码','evidence':[batch['frames'][0]['id']]}]}),encoding='utf-8')
    cli('commit','--file',report)
    period=json.loads(cli('report','--json'));assert period['activities'] and 'continuity' in period
    cli('report','--no-open');assert (data/'reports/report.html').exists()
    result['local_analysis_commit_and_report']=True
    # A production-compiled updater must reject a locally supplied unsigned
    # development package before stopping an existing recorder.
    production=root/'production.exe'
    run(['go','build','-o',production,'./cmd/daylog'])
    run([production,'--data',data,'update','--source',feed,'--force'],expected=1)
    result['production_rejects_development_signer']=True
    # Install/update integrity failures preserve both binary and configuration.
    prior=hashlib.sha256(exe.read_bytes()).hexdigest()
    installer(root/'strict-new',feed,expected=1,strict=True)
    installer(install,feed,expected=1,strict=True)
    assert hashlib.sha256(exe.read_bytes()).hexdigest()==prior and config.read_bytes()==config_before
    bad=root/'bad';bad.mkdir();original=json.loads(feed.read_text())
    archive=bad/'daylog-windows-amd64.zip'
    for name in ['../escaped','daylog.exe']:
        with zipfile.ZipFile(archive,'w') as package:
            package.writestr(name,b'not an executable')
            package.writestr('signature.json',b'{}')
        modified=json.loads(json.dumps(original));modified['artifacts']['windows-amd64']={'url':archive.name,'sha256':hashlib.sha256(archive.read_bytes()).hexdigest()}
        (bad/'release.json').write_text(json.dumps(modified))
        installer(install,bad/'release.json',expected=1)
        assert hashlib.sha256(exe.read_bytes()).hexdigest()==prior
    result['installer_tamper_and_path_rejection']=True
    # The command updating itself is a real mapped Windows executable, which
    # exercises rename behavior that cross-compilation cannot validate.
    check=json.loads(cli('update','--force','--json'))
    assert check['state']=='updated' and json.loads(cli('version','--json'))['version']==version
    assert config.read_bytes()==config_before
    result['self_update_same_version']=True
    # Per-user daemon launch in the runner session. Analysis stays disabled;
    # a locked/service session is expected to skip native screenshots.
    launched=json.loads(cli('start','--json'));pid=launched['status']['pid']
    same=json.loads(cli('start','--json'));assert same['status']['pid']==pid
    check=json.loads(cli('update','--force','--json'));assert check['restarted']
    next_status=wait_status(lambda s:s['pid']!=pid and s.get('updated_at'))
    cli('stop')
    result['background_start_and_update_restart']=True
    result['native_capture_state']=next_status.get('error','available_but_not_user_desktop_validated')
finally:
    if exe.exists():
        subprocess.run([str(exe),'--data',str(data),'stop'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=20)
    if process and process.poll() is None:
        process.terminate();process.wait(timeout=10)
    with winreg.OpenKey(winreg.HKEY_CURRENT_USER,'Environment',0,winreg.KEY_SET_VALUE) as key:
        if old_path is None:
            try:winreg.DeleteValue(key,'Path')
            except FileNotFoundError:pass
        else:winreg.SetValueEx(key,'Path',0,old_path_kind,old_path)
    # Only this test's installation, images and private data are removed.
    shutil.rmtree(root)
    result['temporary_resources_removed']=not root.exists()
print(json.dumps(result,ensure_ascii=False,indent=2))
