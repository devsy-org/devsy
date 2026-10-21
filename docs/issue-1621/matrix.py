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
        and not row.get('harnessError')
        and not row.get('harnessStderr')
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


def captured_text(value):
    if isinstance(value, bytes):
        return value.decode('utf-8', errors='replace')
    return value or ''


def run_cell(cmd, env):
    exit_code, output, diagnostics, failure = None, '', '', ''
    try:
        child = subprocess.run(cmd, env=env, capture_output=True, timeout=30)
        exit_code = child.returncode
        output, diagnostics = captured_text(child.stdout), captured_text(child.stderr)
    except subprocess.TimeoutExpired as error:
        output, diagnostics = captured_text(error.stdout), captured_text(error.stderr)
        failure = f'harness timed out after {error.timeout} seconds'
    except OSError as error:
        failure = f'harness could not start: {error}'

    result = {}
    try:
        decoded = json.loads(output)
        if not isinstance(decoded, dict):
            raise ValueError('harness JSON is not an object')
        result = decoded
        if not all(isinstance(result.get(key), str) for key in ('rawStdout', 'stdout')):
            raise ValueError('harness JSON requires string rawStdout and stdout fields')
    except ValueError as error:
        failure = '; '.join(filter(None, (failure, str(error))))

    raw = result.pop('rawStdout', output)
    result.pop('stdout', None)
    result.update(harnessExit=exit_code, harnessError=failure,
                  harnessStderr=diagnostics, rawPrefix=(raw if isinstance(raw, str) else output)[:30])
    if failure:
        result['harnessOutput'] = output
    return result


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
                row = {**run_cell(cmd, env), 'shell': shell, 'builtin': builtin, 'phase': phase}
                rows.append(row)
                print(json.dumps(row), flush=True)
    a.output.write_text(json.dumps(rows, indent=2) + '\n')
    if not a.baseline:
        require_fixed_rows(rows)


if __name__ == '__main__':
    main()
