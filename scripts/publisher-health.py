#!/usr/bin/env python3
"""Persist an allowlisted status record; never expose keys or command output."""
import datetime
import json
import os
import pathlib
import subprocess
import sys

base = pathlib.Path('/home/ubuntu/pscaper/data/publisher')
path = base / 'status.json'
now = datetime.datetime.now(datetime.timezone.utc).isoformat().replace('+00:00','Z')
try:
    old = json.loads(path.read_text())
except (OSError, ValueError):
    old = {}
result = sys.argv[1] if len(sys.argv) > 1 else 'failed'
commit = subprocess.run(['git','-C',str(base/'repository'),'rev-parse','--short=12','HEAD'],
                        capture_output=True,text=True,timeout=8)
entry = {
    'last_attempt': now,
    'last_success': now if result == 'ok' else old.get('last_success'),
    'state': result,
    'commit': commit.stdout.strip() if commit.returncode == 0 else old.get('commit',''),
}
temp=path.with_suffix('.next')
temp.write_text(json.dumps(entry,separators=(',',':'))+'\n')
os.replace(temp,path)
