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
parser.add_argument('--skip-native-check', action='store_true', help='explicit fixture-only fallback when LaunchServices is unavailable')
args = parser.parse_args()
if args.skip_native_check and args.live_agent:
    parser.error('--skip-native-check cannot be combined with --live-agent')
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
    run(['sh', repo / 'skills/daylog/scripts/install-runtime.sh', '--apps-dir', root / 'Applications', '--bin-dir', root / 'bin', '--data-dir', data, '--source', repo / 'skills/daylog/assets/release.json'])
    exe = root / 'bin/daylog'
    def cli(*arguments, **kw):
        return run([exe, '--data', data, *arguments], **kw)
    result['installed_version'] = json.loads(cli('version', '--json'))['version']
    run(['sh', repo / 'skills/daylog/scripts/install-runtime.sh', '--apps-dir', root / 'Applications', '--bin-dir', root / 'bin', '--data-dir', data, '--source', repo / 'skills/daylog/assets/release.json'])
    result['repeat_install_safe'] = True
    assert cli('version').startswith('Daylog ')
    assert '当前配置' in cli('config')
    assert '本机 Agent' in cli('agents')
    assert '没有待整理' in cli('pending')
    result['default_queries_are_human_readable'] = True
    fresh = root / 'fresh'
    for help_args in ([], ['-h'], ['--help']):
        text = run([exe, '--data', fresh, *help_args])
        assert 'daylog start' in text and 'daylog today' in text and not fresh.exists()
    result['help_aliases_without_data_side_effects'] = True
    text = run([exe, '--data', fresh, 'today'])
    assert 'daylog start' in text and (fresh / 'config.json').is_file() and not (fresh / 'run.sock').exists()
    result['today_initializes_without_recording'] = True
    run([exe, '--data', fresh, 'config', '--endpoint', 'https://api.example.com/v1/chat/completions', '--model', 'fixture-vision'])
    configured = json.loads(run([exe, '--data', fresh, 'config', '--json']))
    assert configured['ai']['enabled'] and configured['ai']['model'] == 'fixture-vision'
    run([exe, '--data', fresh, 'config', '--agent', 'auto'])
    assert not json.loads(run([exe, '--data', fresh, 'config', '--json']))['ai']['enabled']
    result['llm_config_cli_roundtrip'] = True
    run([exe, '--data', fresh, 'config', '--profile', 'battery'])
    profile = json.loads(run([exe, '--data', fresh, 'config', '--json']))
    assert profile['min_save_seconds'] == 120 and profile['max_images_per_day'] == 768
    assert profile['ai']['model'] == 'fixture-vision' and not profile['ai']['enabled']
    result['profile_preserves_provider'] = True
    assert 'service install' not in cli('help') and 'service install' in cli('help', '--advanced')
    if args.skip_native_check:
        result['native_query'] = 'skipped_explicitly_not_verified'
    else:
        doctor = json.loads(cli('doctor', '--json'))
        result['power'] = {k: doctor['environment'][k] for k in ['on_battery', 'battery_percent', 'low_power', 'thermal', 'screen_permission']}
    fixture = root / 'synthetic.jpg'
    run(['go', 'run', './scripts/fixture', fixture])
    proc = subprocess.Popen([str(exe), '--data', str(data), 'run', '--fixture', str(fixture)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, cwd=repo)
    deadline = time.monotonic() + 10
    while True:
        try:
            status = json.loads(cli('status', '--json'))
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
    assert '暂停' in cli('pause', '--minutes', '0')
    deadline = time.monotonic() + 5
    while not json.loads(cli('status', '--json'))['paused']:
        if time.monotonic() > deadline:
            raise RuntimeError('pause did not apply')
        time.sleep(0.05)
    assert '恢复' in cli('resume')
    deadline = time.monotonic() + 5
    while json.loads(cli('status', '--json'))['paused']:
        if time.monotonic() > deadline:
            raise RuntimeError('resume did not apply')
        time.sleep(0.05)
    cli('stop')
    proc.wait(timeout=15)
    cli('stop')
    result['pause_resume_stop'] = True
    assert not (data / 'run.sock').exists()
    assert '待整理' in cli('pending')
    pending = json.loads(cli('pending', '--limit', '1', '--json'))
    assert len(pending) == 1
    batch = pending[0]['batch']
    assert len(Path(batch['frames'][0]['file']).parts) == 3
    result['frames_grouped_by_date'] = True
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
        if batch.get('schema_version') == 2:
            report = {'schema_version': 2, 'batch_id': batch['id'], 'segments': [{'title': '匿名夹具记录', 'summary': '合成界面中的测试文字。', 'category': '其他', 'object': '测试页面', 'actions': ['查看'], 'confidence': 'medium', 'task_id': '', 'evidence': [batch['frames'][0]['id']], 'uncertainties': []}]}
        report_file = root / 'report.json'
        report_file.write_text(json.dumps(report, ensure_ascii=False))
        report_file.chmod(0o600)
        assert '活动记录已保存' in cli('commit', '--file', report_file)
    timeline = json.loads(cli('today', '--json'))
    assert timeline['reports'] and timeline['pending_batches'] == 0
    result['report_commit_and_query'] = True
    result['report_source'] = timeline['reports'][0]['source']
    overview = json.loads(cli('report', '--period', 'week', '--json'))
    assert overview['period'] == 'week' and overview['activities'] and overview['pending_batches'] == 0
    cli('report', '--period', 'month', '--no-open')
    html = data / 'reports/report.html'
    assert html.is_file() and html.stat().st_mode & 0o777 == 0o600
    assert 'report-data' in html.read_text()
    assert '只描述可见事实' in cli('prompt')
    result['period_report_and_private_html'] = True
    ranged = json.loads(cli('report', '--from', batch['start'], '--to', batch['end'], '--json'))
    assert ranged['period'] == 'range' and ranged['analyzed_batches'] == 1
    assert ranged['estimated_seconds'] == 0 and ranged['unknown_seconds'] >= 1
    cli('report', '--from', batch['start'], '--to', batch['end'], '--no-open')
    assert 'data-initial="range"' in html.read_text()
    result['range_json_html_unknown_duration'] = True
    plist = root / 'service.plist'
    plist.write_text(cli('service', 'install', '--dry-run'))
    run(['/usr/bin/plutil', '-lint', plist])
    result['launchagent_plist'] = 'valid_not_loaded'
    check = json.loads(cli('update', '--check', '--json'))
    assert not check['upgrade_available']
    result['update_reinstall'] = json.loads(cli('update', '--force', '--json'))['state']
    assert json.loads(cli('version', '--json'))['version'] == result['installed_version']
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
