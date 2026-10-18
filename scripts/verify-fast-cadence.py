#!/usr/bin/env python3
"""Isolated 10-second synthetic recording; no capture permission/model calls."""
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time

exe = Path(sys.argv[1]).resolve()
repo = Path(__file__).resolve().parents[1]
root = Path(tempfile.mkdtemp(prefix='daylog-fast-cadence-', dir='/tmp'))
process = None

def cli(*args):
    p = subprocess.run([str(exe), '--data', str(root/'data'), *args], capture_output=True, text=True, timeout=20)
    if p.returncode:
        raise RuntimeError(p.stderr)
    return p.stdout

def wait(predicate, timeout=25):
    end = time.monotonic()+timeout
    while time.monotonic()<end:
        status=json.loads(cli('status','--json'))
        if predicate(status): return status
        time.sleep(.15)
    raise AssertionError('fixture did not reach expected state')

try:
    cli('init')
    config=root/'data/config.json'
    c=json.loads(config.read_text());c['interval_seconds']=10;c['min_save_seconds']=10;c['batch_seconds']=300;c['agent']['enabled']=False;c['ai']['enabled']=False
    config.write_text(json.dumps(c))
    image=root/'fixture.jpg'
    subprocess.run(['go','run','./scripts/fixture',str(image)],cwd=repo,check=True,capture_output=True)
    process=subprocess.Popen([str(exe),'--data',str(root/'data'),'run','--fixture',str(image)],stdout=subprocess.DEVNULL,stderr=subprocess.PIPE,text=True)
    initial=wait(lambda s:s.get('captures',0)>=2)
    cli('pause','--minutes','30')
    end=time.monotonic()+5
    while not json.loads(cli('pending','--json')):
        assert time.monotonic()<end
        time.sleep(.1)
    batches=json.loads(cli('pending','--json'))
    assert len(batches)==1 and batches[0]['batch']['max_gap_seconds']==20
    assert json.loads(cli('status','--json'))['paused']
    cli('resume')
    wait(lambda s:s.get('saved_frames',0)>=2)
    cli('stop');process.wait(timeout=15)
    assert process.returncode==0, process.stderr.read()
    batches=json.loads(cli('pending','--json'))
    assert len(batches)==2
    assert not (root/'data/draft.json').exists()
    print(json.dumps({'ten_second_config':True,'pause_sealed':True,'resume_stop_sealed':True,'batches':2,'max_gap_seconds':20,'native_capture':False,'model_calls':0}))
finally:
    if process is not None and process.poll() is None:
        process.terminate();process.wait(timeout=15)
    shutil.rmtree(root)
