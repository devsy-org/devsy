#!/usr/bin/env python3
"""Run generated core injection through the actual SSH-provider executable."""
import argparse
import json
import os
from pathlib import Path
import shlex
import subprocess


def fixed_row_passes(row):
    expected_requests = {'cold': 1, 'warm': 0, 'forced-replacement': 1}.get(row.get('phase'))
    requests = row.get('localRequests')
    return (
        expected_requests is not None
        and row.get('harnessExit') == 0
        and row.get('executed') is True
        and row.get('duplexExact') is True
        and row.get('error') == ''
        and row.get('execError') == ''
        and row.get('stderr') == ''
        and isinstance(requests, int)
        and not isinstance(requests, bool)
        and requests == expected_requests
    )


def require_fixed_rows(rows):
    failures = [row for row in rows if not fixed_row_passes(row)]
    if failures:
        raise SystemExit('SSH matrix failed: ' + json.dumps(failures))


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--state', required=True, type=Path)
    p.add_argument('--core', required=True, type=Path)
    p.add_argument('--provider', required=True, type=Path)
    p.add_argument('--output', required=True, type=Path)
    p.add_argument('--baseline', action='store_true')
    p.add_argument('--hostile', action='store_true')
    a = p.parse_args()
    fixture = json.loads((a.state / 'fixture.json').read_text())
    env = dict(os.environ, PATH=fixture['bin'] + ':' + os.environ['PATH'])
    payload = a.state / 'duplex-agent'
    payload.write_text('''#!/usr/bin/python3
import sys
r = sys.stdin.buffer
w = sys.stdout.buffer
w.write(r.read(4096) + b"READY\\n")
w.flush()
if r.readline() == b"LIVE-FOLLOWON\\n":
    w.write(b"LIVE-OK\\n")
    w.flush()
''')
    input_file = a.state / 'input.bin'
    data = bytearray(os.urandom(4096))
    data[:8] = b'\x00\'"$`\n\r\\'
    input_file.write_bytes(data)
    rows = []
    for shell in (['bash', 'zsh', 'fish'] if a.baseline else ['bash', 'zsh', 'fish', 'dash', 'ash']):
        for builtin in ['false', 'true']:
            suffix = f'/literal space\'"$`(quote)\ncarriage\rsemi;{builtin}' if a.hostile else f'/matrix-{builtin}'
            dest = f'/home/fixture-{shell}' + suffix + '/agent'
            for phase in (['cold'] if a.baseline else ['cold', 'warm', 'forced-replacement']):
                cmd = [str(a.core.resolve()), '-provider', str(a.provider.resolve()),
                       '-host', 'fixture-' + shell, '-port', str(fixture['port']),
                       '-builtin', builtin, '-dest', dest, '-command', shlex.quote(dest),
                       '-payload', str(payload), '-input', str(input_file),
                       '-check', 'false' if phase == 'warm' else 'true']
                c = subprocess.run(cmd, env=env, capture_output=True, text=True, timeout=30)
                row = {**json.loads(c.stdout), 'shell': shell, 'builtin': builtin,
                       'phase': phase, 'harnessExit': c.returncode}
                row['rawPrefix'] = row.pop('rawStdout')[:30]
                row.pop('stdout')
                rows.append(row)
                print(json.dumps(row), flush=True)
    a.output.write_text(json.dumps(rows, indent=2) + '\n')
    if not a.baseline:
        require_fixed_rows(rows)


if __name__ == '__main__':
    main()
