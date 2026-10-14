#!/usr/bin/env python3
"""Verify a fixed-publisher release ZIP on Windows, without capture or AI."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import winreg

repo=Path(__file__).resolve().parents[1]
assets=Path(sys.argv[1]).resolve()
env={**os.environ,'DAYLOG_DEVELOPMENT_INSTALL':'0'}
with winreg.OpenKey(winreg.HKEY_CURRENT_USER,'Environment') as key:
    try:prior_path,prior_type=winreg.QueryValueEx(key,'Path')
    except FileNotFoundError:prior_path,prior_type=None,None
try:
    with tempfile.TemporaryDirectory(prefix='daylog signed ') as directory:
        root=Path(directory);package=root/'daylog-windows-amd64.zip'
        shutil.copyfile(assets/package.name,package)
        feed=json.loads((assets/'release.json').read_text());feed['artifacts']['windows-amd64']['url']=package.name
        manifest=root/'release.json';manifest.write_text(json.dumps(feed))
        target=root/'bin'
        def run(args,success=True):
            p=subprocess.run([str(x) for x in args],cwd=repo,env=env,capture_output=True,text=True,encoding='utf-8',errors='replace',timeout=90)
            assert (p.returncode==0)==success,p.stderr[-1800:]+p.stdout[-1000:]
            return p.stdout
        for shell in ['pwsh','powershell.exe']:
            run([shell,'-NoProfile','-NonInteractive','-File',repo/'skills/daylog/scripts/install-runtime.ps1','-Source',manifest,'-InstallDir',target])
        exe=target/'daylog.exe';data=root/'data'
        assert json.loads(run([exe,'version','--json']))['version']==feed['version']
        run([exe,'--data',data,'init'])
        before=(data/'config.json').read_bytes()
        result=json.loads(run([exe,'--data',data,'update','--source',manifest,'--force','--json']))
        assert result['state']=='updated'
        digest=hashlib.sha256(exe.read_bytes()).hexdigest()
        # Binding the binary to a version prevents a feed from relabelling it.
        feed['version']='999.0.0';manifest.write_text(json.dumps(feed))
        run([exe,'--data',data,'update','--source',manifest],success=False)
        assert hashlib.sha256(exe.read_bytes()).hexdigest()==digest and (data/'config.json').read_bytes()==before
finally:
    with winreg.OpenKey(winreg.HKEY_CURRENT_USER,'Environment',0,winreg.KEY_SET_VALUE) as key:
        if prior_path is None:
            try:winreg.DeleteValue(key,'Path')
            except FileNotFoundError:pass
        else:winreg.SetValueEx(key,'Path',0,prior_type,prior_path)
print('PASS: fixed publisher Windows install (PowerShell 5.1/7), mapped-executable self-update, version substitution rejection')
