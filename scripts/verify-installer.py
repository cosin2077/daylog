#!/usr/bin/env python3
"""Exercise the real installer/download/update path against an isolated HTTP feed."""
import hashlib
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import io
import json
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import threading

repo = Path(__file__).resolve().parents[1]
assets = repo / 'skills/daylog/assets'
arch = subprocess.check_output(['go', 'env', 'GOARCH'], text=True).strip()
name = f'daylog-macos-{arch}.tar.gz'
feed = json.loads((assets / 'release.json').read_text())

with tempfile.TemporaryDirectory(prefix='daylog-installer-') as directory:
    root = Path(directory)
    web = root / 'web'
    web.mkdir()
    shutil.copyfile(assets / name, web / name)
    (web / 'release.json').write_text(json.dumps(feed))
    requested = []

    class Handler(SimpleHTTPRequestHandler):
        def __init__(self, *args, **kwargs):
            super().__init__(*args, directory=str(web), **kwargs)

        def log_message(self, *_):
            pass

        def do_GET(self):
            requested.append(self.path)
            super().do_GET()

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    source = f'http://127.0.0.1:{server.server_port}/release.json'

    def install(target, expected=0):
        p = subprocess.run(['sh', str(repo / 'skills/daylog/scripts/install-runtime.sh'),
                            '--source', source, '--apps-dir', str(target / 'Applications'),
                            '--bin-dir', str(target / 'bin'), '--data-dir', str(target / 'data')],
                           capture_output=True, text=True, timeout=90)
        assert (p.returncode == 0) == (expected == 0), p.stderr[-1000:]
        assert not list((target / 'Applications').glob('.daylog-*'))
        return p

    try:
        installed = root / 'installed'
        install(installed)
        exe = installed / 'bin/daylog'
        config = installed / 'data/config.json'
        assert json.loads(config.read_text())['update']['source'] == source
        assert json.loads(subprocess.check_output([str(exe), 'version', '--json']))['version'] == feed['version']
        assert not (installed / 'data/run.sock').exists()
        assert {p for p in requested if p.endswith('.tar.gz')} == {'/' + name}
        assert not (installed / 'Applications/.daylog-install.lock').exists()
        # A repeated installation must retain unrelated preferences and data.
        c = json.loads(config.read_text())
        c['blocked_apps'] = ['test.private.application']
        config.write_text(json.dumps(c))
        install(installed)
        assert json.loads(config.read_text())['blocked_apps'] == c['blocked_apps']

        bad = json.loads(json.dumps(feed))
        bad['artifacts'][f'darwin-{arch}']['sha256'] = '0' * 64
        (web / 'release.json').write_text(json.dumps(bad))
        install(root / 'bad-hash', expected=1)
        assert not (root / 'bad-hash/Applications/Daylog.app').exists()

        for label, member_name, link in [('traversal', '../escaped', False), ('symlink', 'Daylog.app/escape', True)]:
            with tarfile.open(web / name, 'w:gz') as archive:
                member = tarfile.TarInfo(member_name)
                if link:
                    member.type = tarfile.SYMTYPE
                    member.linkname = '/tmp'
                    archive.addfile(member)
                else:
                    member.size = 4
                    archive.addfile(member, io.BytesIO(b'test'))
            bad['artifacts'][f'darwin-{arch}']['sha256'] = hashlib.sha256((web / name).read_bytes()).hexdigest()
            (web / 'release.json').write_text(json.dumps(bad))
            install(root / label, expected=1)
            assert not (root / label / 'Applications/Daylog.app').exists()

        (web / 'release.json').unlink()
        result = install(root / 'missing-release', expected=1)
        assert 'Release' in result.stderr
        # Skill installation from source must work without bundled binaries.
        lean = root / 'lean-skill'
        shutil.copytree(repo / 'skills/daylog', lean, ignore=shutil.ignore_patterns('assets'))
        subprocess.run(['python3', str(repo / 'scripts/install-skills.py'), '--source', str(lean),
                        '--agent', 'all', '--home', str(root / 'agent-home')], check=True, capture_output=True)
        assert len(list((root / 'agent-home').rglob('SKILL.md'))) == 3
        print('PASS: HTTP download, arch selection, persisted feed, repeated install, checksum/path/link rejection, missing-release guidance, lean skills')
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=5)
print('Installer fixtures and HTTP server removed')
