#!/usr/bin/env python3
"""Isolated binary, lifecycle, update and skill-install smoke; no real capture."""
import argparse
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import time

parser = argparse.ArgumentParser()
parser.add_argument('--live-agent', choices=['auto', 'claude', 'codex', 'hermes'], help='optional anonymous vision call; honors real power policy')
args = parser.parse_args()
repo = Path(__file__).resolve().parents[1]
root = Path(tempfile.mkdtemp(prefix='daylog-verify-', dir='/tmp'))
data = root / 'data'
proc = None
result = {'fixture_only': True}

def run(command, *, timeout=30, expected=0):
    p = subprocess.run([str(x) for x in command], text=True, capture_output=True, timeout=timeout, cwd=repo)
    if p.returncode != expected:
        raise RuntimeError(f'{command[0]} failed with exit {p.returncode}: {p.stderr[:300]}')
    return p.stdout

try:
    run(['sh', repo / 'skills/daylog/scripts/install-runtime.sh', '--apps-dir', root / 'Applications', '--bin-dir', root / 'bin', '--data-dir', data])
    exe = root / 'bin/daylog'
    def cli(*arguments, **kw):
        return run([exe, '--data', data, *arguments], **kw)
    result['installed_version'] = json.loads(cli('version'))['version']
    doctor = json.loads(cli('doctor'))
    result['power'] = {k: doctor['environment'][k] for k in ['on_battery', 'battery_percent', 'low_power', 'thermal', 'screen_permission']}
    fixture = root / 'synthetic.jpg'
    run(['go', 'run', './scripts/fixture', fixture])
    proc = subprocess.Popen([str(exe), '--data', str(data), 'run', '--fixture', str(fixture)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, cwd=repo)
    deadline = time.monotonic() + 10
    while True:
        try:
            status = json.loads(cli('status'))
            if status['saved_frames'] == 1:
                break
        except RuntimeError:
            pass
        if time.monotonic() > deadline:
            raise RuntimeError('fixture daemon did not become ready')
        time.sleep(0.05)
    run([exe, '--data', data, 'run', '--fixture', fixture], expected=1)
    result['single_instance'] = True
    samples = []
    for _ in range(5):
        time.sleep(3)
        parts = run(['/bin/ps', '-o', 'rss=,pcpu=,nice=', '-p', str(proc.pid)]).split()
        samples.append({'rss_kib': int(parts[0]), 'cpu_percent': float(parts[1]), 'nice': int(parts[2])})
    result['idle_fixture_samples'] = samples
    cli('pause', '--minutes', '0')
    deadline = time.monotonic() + 5
    while not json.loads(cli('status'))['paused']:
        if time.monotonic() > deadline:
            raise RuntimeError('pause did not apply')
        time.sleep(0.05)
    cli('resume')
    deadline = time.monotonic() + 5
    while json.loads(cli('status'))['paused']:
        if time.monotonic() > deadline:
            raise RuntimeError('resume did not apply')
        time.sleep(0.05)
    cli('stop')
    proc.wait(timeout=15)
    result['pause_resume_stop'] = True
    assert not (data / 'run.sock').exists()
    pending = json.loads(cli('pending', '--limit', '1'))
    assert len(pending) == 1
    batch = pending[0]['batch']
    if args.live_agent:
        if doctor['environment']['on_battery'] or doctor['environment']['thermal'] > 0 or doctor['environment']['low_power']:
            result['live_agent'] = 'deferred_by_power_policy'
        else:
            command = ['analyze', '--batch', batch['id']]
            if args.live_agent != 'auto':
                command += ['--agent', args.live_agent]
            try:
                cli(*command, timeout=300)
                result['live_agent'] = 'anonymous_image_analyzed'
            except RuntimeError as exc:
                result['live_agent'] = 'failed'
                result['live_agent_error'] = str(exc)
    if not result.get('live_agent') == 'anonymous_image_analyzed':
        report = {'batch_id': batch['id'], 'cards': [{'start': batch['start'], 'end': batch['end'], 'title': '匿名夹具记录', 'summary': '合成界面中的测试文字。', 'category': '测试', 'evidence': [batch['frames'][0]['id']]}]}
        report_file = root / 'report.json'
        report_file.write_text(json.dumps(report, ensure_ascii=False))
        report_file.chmod(0o600)
        cli('commit', '--file', report_file)
    timeline = json.loads(cli('today', '--json'))
    assert timeline['reports'] and timeline['pending_batches'] == 0
    result['report_commit_and_query'] = True
    result['report_source'] = timeline['reports'][0]['source']
    plist = root / 'service.plist'
    plist.write_text(cli('service', 'install', '--dry-run'))
    run(['/usr/bin/plutil', '-lint', plist])
    result['launchagent_plist'] = 'valid_not_loaded'
    check = json.loads(cli('update', '--check'))
    assert not check['upgrade_available']
    result['update_reinstall'] = json.loads(cli('update', '--force'))['state']
    assert json.loads(cli('version'))['version'] == result['installed_version']
    run(['python3', repo / 'scripts/install-skills.py', '--agent', 'all', '--home', root / 'home'])
    result['skill_dirs'] = [str(path.relative_to(root / 'home')) for path in (root / 'home').rglob('SKILL.md')]
    assert len(result['skill_dirs']) == 3
finally:
    if proc is not None and proc.poll() is None:
        proc.terminate()
        try:
            proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=5)
    shutil.rmtree(root)
    result['temporary_resources_removed'] = not root.exists()
print(json.dumps(result, ensure_ascii=False, indent=2))
