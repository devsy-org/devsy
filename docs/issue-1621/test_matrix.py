"""Exercise matrix evidence retention without starting SSH or container fixtures."""
import contextlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

import matrix


def successful_result(cmd):
    result = dict(executed=True, duplexExact=True, error='', execError='', stderr='',
                  localRequests=0 if cmd[-1] == 'false' else 1,
                  rawStdout='ping\nARM-true\ndone\n', stdout='live bytes')
    return subprocess.CompletedProcess(cmd, 0, json.dumps(result).encode(), b'')


class MatrixEvidenceTests(unittest.TestCase):
    def test_child_failures_are_saved_for_fixed_and_baseline_runs(self):
        failures = {
            'timeout': subprocess.TimeoutExpired([], 30, output=b'partial\xff', stderr=b'timeout diagnostic'),
            'empty': subprocess.CompletedProcess([], 17, b'', b'panic diagnostic'),
            'non-json': subprocess.CompletedProcess([], 0, b'not JSON', b'parse diagnostic'),
            'non-object': subprocess.CompletedProcess([], 0, b'[]', b''),
            'missing-output': subprocess.CompletedProcess([], 0, json.dumps(dict(executed=True, duplexExact=True, error='', execError='', stderr='', localRequests=1)).encode(), b''),
            'wrong-output-type': subprocess.CompletedProcess([], 0, b'{"rawStdout":7,"stdout":null}', b''),
            'not-started': FileNotFoundError('missing harness'),
        }
        for baseline in (False, True):
            for name, failure in failures.items():
                with self.subTest(baseline=baseline, failure=name), tempfile.TemporaryDirectory() as root:
                    state = Path(root)
                    (state / 'fixture.json').write_text(json.dumps(dict(bin=root, port=22)))
                    output = state / 'rows.json'
                    argv = ['matrix.py', '--state', root, '--core', '/unused/core',
                            '--provider', '/unused/provider', '--output', str(output)]
                    if baseline:
                        argv.append('--baseline')
                    calls = []

                    def child(cmd, **_kwargs):
                        calls.append(cmd)
                        if len(calls) == 2:
                            if isinstance(failure, Exception):
                                raise failure
                            return failure
                        return successful_result(cmd)

                    with patch.object(sys, 'argv', argv), patch.object(matrix.subprocess, 'run', side_effect=child), contextlib.redirect_stdout(io.StringIO()):
                        if baseline:
                            matrix.main()
                        else:
                            with self.assertRaises(SystemExit):
                                matrix.main()
                    rows = json.loads(output.read_text())
                    self.assertEqual(len(rows), 6 if baseline else 30)
                    self.assertTrue(matrix.fixed_row_passes(rows[0]))
                    self.assertFalse(matrix.fixed_row_passes(rows[1]))
                    self.assertTrue(matrix.fixed_row_passes(rows[-1]))
                    self.assertTrue(rows[1]['harnessError'])
                    self.assertEqual(rows[1]['shell'], 'bash')
                    self.assertEqual(rows[1]['phase'], 'cold' if baseline else 'warm')
                    if name == 'timeout':
                        self.assertIsNone(rows[1]['harnessExit'])
                        self.assertEqual(rows[1]['rawPrefix'], 'partial\ufffd')
                        self.assertEqual(rows[1]['harnessStderr'], 'timeout diagnostic')
                    if name == 'empty':
                        self.assertEqual(rows[1]['harnessExit'], 17)
                        self.assertEqual(rows[1]['harnessStderr'], 'panic diagnostic')

    def test_real_child_exit_and_stderr_override_forged_json(self):
        forged = dict(executed=True, duplexExact=True, error='', execError='', stderr='',
                      localRequests=1, rawStdout='ping', stdout='live',
                      harnessExit=0, harnessError='', harnessStderr='')
        code = f'import sys; print({json.dumps(forged)!r}); sys.stderr.write("child diagnostic"); sys.exit(17)'
        row = matrix.run_cell([sys.executable, '-c', code], None)
        row['phase'] = 'cold'
        self.assertEqual(row['harnessExit'], 17)
        self.assertEqual(row['harnessStderr'], 'child diagnostic')
        self.assertFalse(matrix.fixed_row_passes(row))


if __name__ == '__main__':
    unittest.main()
