#!/usr/bin/env python3
"""Install a self-contained skill into the selected local agent's directory."""
import argparse
import json
from pathlib import Path
import shutil

parser = argparse.ArgumentParser()
parser.add_argument('--agent', choices=['codex', 'claude', 'hermes', 'all'], default='all')
parser.add_argument('--home', type=Path, default=Path.home(), help='alternate home for isolated verification')
parser.add_argument('--source', type=Path, default=Path(__file__).resolve().parents[1] / 'skills/daylog')
args = parser.parse_args()
paths = {'codex': '.agents/skills', 'claude': '.claude/skills', 'hermes': '.hermes/skills'}
chosen = list(paths) if args.agent == 'all' else [args.agent]
if not (args.source / 'SKILL.md').is_file() or not (args.source / 'scripts/install-runtime.sh').is_file():
    parser.error('source must contain the Daylog skill and installer')
targets = [args.home / paths[agent] / 'daylog' for agent in chosen]
for target in targets:
    if target.exists() or target.is_symlink():
        parser.error(f'refusing to overwrite an existing skill: {target}')
for target in targets:
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copytree(args.source, target)
print(json.dumps({'skills': [str(path) for path in targets], 'runtime_started': False}, ensure_ascii=False))
