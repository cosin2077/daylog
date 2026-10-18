#!/usr/bin/env python3
"""Opt-in macOS LaunchAgent update check using only synthetic images.

Creates a private temporary install and one task-owned LaunchAgent. Requires a
logged-in GUI session; does not request recording permission or call any model.
"""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import plistlib
import shutil
import subprocess
import tempfile
import time

repo = Path(__file__).resolve().parents[1]
os.environ['DAYLOG_DEVELOPMENT_INSTALL'] = '1'
root = Path(tempfile.mkdtemp(prefix="daylog-launchd-update-", dir="/tmp"))
data = root / "data"
exe = root / "bin/daylog"
plist = None

def run(args, timeout=30):
    p = subprocess.run([str(a) for a in args], cwd=repo, text=True, capture_output=True, timeout=timeout)
    if p.returncode:
        raise RuntimeError(f"command failed ({p.returncode}): {p.stderr[-400:]}")
    return p.stdout

def cli(*args, **kwargs):
    return run([exe, "--data", data, *args], **kwargs)

def ready(paused=False):
    until = time.monotonic() + 15
    while time.monotonic() < until:
        try:
            status = json.loads(cli("status", "--json"))
            if status.get("updated_at") and status.get("paused") == paused and (paused or status.get("saved_frames", 0) > 0):
                return status
        except RuntimeError:
            pass
        time.sleep(0.1)
    raise RuntimeError("LaunchAgent did not publish fixture capture status")

try:
    run(["sh", repo / "skills/daylog/scripts/install-runtime.sh", "--apps-dir", root / "Applications", "--bin-dir", root / "bin", "--data-dir", data, "--source", repo / "skills/daylog/assets/release.json"])
    config = json.loads((data / "config.json").read_text())
    config["agent"]["enabled"] = False
    config["ai"]["enabled"] = False
    (data / "config.json").write_text(json.dumps(config))
    fixture = root / "synthetic.jpg"
    run(["go", "run", "./scripts/fixture", fixture])
    installed = json.loads(cli("service", "install", "--json"))
    plist = Path(installed["plist"])
    service = plistlib.loads(plist.read_bytes())
    service["ProgramArguments"] += ["--fixture", str(fixture)]
    plist.write_bytes(plistlib.dumps(service))
    cli("service", "start")
    before = ready()
    digest = hashlib.sha256((data / "config.json").read_bytes()).hexdigest()
    for paused in (False, True, False):
        if paused:
            cli("pause", "--minutes", "30")
        elif before.get("paused"):
            cli("resume")
        before = ready(paused)
        pause_until = before.get("pause_until")
        result = json.loads(cli("update", "--force", "--json", timeout=60))
        assert result["state"] == "updated" and result["restarted"]
        after = ready(paused)
        if paused:
            assert after["saved_frames"] == 0, "update captured before restoring pause"
            assert after["pause_until"] == pause_until, "update lost pause deadline"
        assert after["pid"] != before["pid"], "old PID survived update"
        assert after["started_at"] > before["started_at"], "old status mistaken for restart"
        assert hashlib.sha256((data / "config.json").read_bytes()).hexdigest() == digest
        before = after
    print("PASS: three real LaunchAgent update swaps, preserved pause before first capture, new PIDs, published status and unchanged config")
finally:
    if plist is not None:
        label = plist.stem
        subprocess.run(["/bin/launchctl", "bootout", f"gui/{os.getuid()}/{label}"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        deadline = time.monotonic() + 10
        stopped = False
        while time.monotonic() < deadline:
            check = subprocess.run(["/bin/launchctl", "print", f"gui/{os.getuid()}/{label}"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            with (data / "daemon.lock").open("a") as lock:
                try:
                    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    stopped = check.returncode != 0
                    fcntl.flock(lock, fcntl.LOCK_UN)
                except BlockingIOError:
                    stopped = False
            if stopped:
                break
            time.sleep(0.1)
        if not stopped:
            raise RuntimeError("test LaunchAgent still stopping; temporary installation retained")
        plist.unlink(missing_ok=True)
    shutil.rmtree(root)
    print("Task-owned LaunchAgent, temporary app and synthetic data removed")
