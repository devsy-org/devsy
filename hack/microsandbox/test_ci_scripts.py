import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parent
LABEL = "up-provider-microsandbox-images"
EXPECTED_HASH = "b3cc4a5e3f52dfdd938a6f67ac4a9a959ddfe304bab56de4964044b8613f01bb"
RELEASE_URL = (
    "https://github.com/superradcompany/microsandbox/releases/download/"
    "v0.7.7/microsandbox-linux-x86_64.tar.gz"
)


class MicroSandboxCIScriptTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name) / "repo"
        self.hack = self.root / "hack" / "microsandbox"
        self.hack.mkdir(parents=True)
        (self.root / "e2e").mkdir()
        shutil.copy(ROOT / "install-linux.sh", self.hack)
        shutil.copy(ROOT / "run-ci.sh", self.hack)
        shutil.copy(ROOT / "verify_report.py", self.hack)
        self.bin = Path(self.temp.name) / "bin"
        self.bin.mkdir()
        self.runner_temp = Path(self.temp.name) / "runner"
        self.runner_temp.mkdir()
        self.home = Path(self.temp.name) / "home"
        self.home.mkdir()
        self.github_path = Path(self.temp.name) / "github-path"
        self.github_path.touch()
        self.sudo_capture = Path(self.temp.name) / "sudo-calls"
        self._write_stub(
            "sudo",
            """
            #!/usr/bin/env python3
            import os
            import sys

            args = sys.argv[1:]
            names = []
            while args and "=" in args[0] and not args[0].startswith("-"):
                name, value = args.pop(0).split("=", 1)
                os.environ[name] = value
                names.append(name)
            capture = os.environ.get("FAKE_SUDO_CAPTURE")
            if capture:
                with open(capture, "a", encoding="utf-8") as output:
                    output.write("command=" + (args[0] if args else "") + ";env=" + ",".join(names) + "\\n")
            if args[:2] in (["test", "-r"], ["test", "-w"]):
                sys.exit(int(os.environ.get("FAKE_KVM_EXIT", "0")))
            if args and args[0] == "chown":
                sys.exit(int(os.environ.get("FAKE_CHOWN_EXIT", "0")))
            if not args:
                sys.exit(2)
            os.execvpe(args[0], args, os.environ)
            """,
        )
        self._write_stub(
            "tee",
            """
            #!/usr/bin/env python3
            import os
            import sys

            data = sys.stdin.buffer.read()
            with open(sys.argv[1], "wb") as output:
                output.write(data)
            sys.stdout.buffer.write(data)
            sys.exit(int(os.environ.get("FAKE_TEE_EXIT", "0")))
            """,
        )
        self._write_stub(
            "curl",
            """
            #!/usr/bin/env python3
            import os
            import pathlib
            import sys

            args = sys.argv[1:]
            target = pathlib.Path(args[args.index("-o") + 1])
            target.write_bytes(b"fixture archive")
            with open(os.environ["FAKE_CURL_CAPTURE"], "a", encoding="utf-8") as output:
                output.write(args[-1] + "\\n")
            """,
        )
        self._write_stub(
            "sha256sum",
            """
            #!/usr/bin/env python3
            import os
            import sys

            line = sys.stdin.readline()
            expected = os.environ["FAKE_EXPECTED_HASH"]
            if not line.startswith(expected + "  "):
                sys.exit(8)
            if os.environ.get("FAKE_SHA256_FAIL") == "1":
                sys.exit(9)
            print(line.split()[-1] + ": OK")
            """,
        )
        self._write_stub(
            "tar",
            """
            #!/usr/bin/env python3
            import pathlib
            import sys

            args = sys.argv[1:]
            target = pathlib.Path(args[args.index("-C") + 1])
            msb = target / "msb"
            msb.write_text("#!/bin/sh\\necho MicroSandbox v0.7.7\\n", encoding="utf-8")
            msb.chmod(0o755)
            (target / "libkrunfw.so.5.6.1").write_bytes(b"test library")
            """,
        )
        self._write_stub(
            "e2e.test",
            """
            #!/usr/bin/env python3
            import json
            import os
            import pathlib
            import sys

            print("fixture E2E output")
            if os.environ.get("FAKE_SKIP_REPORT") != "1":
                report = next(arg.split("=", 1)[1] for arg in sys.argv if arg.startswith("-ginkgo.json-report="))
                label = next(arg.split("=", 1)[1] for arg in sys.argv if arg.startswith("--ginkgo.label-filter="))
                specs = []
                for provider in ("microsandbox-builtin", "microsandbox-external"):
                    specs.append({
                        "ContainerHierarchyLabels": [[label]],
                        "LeafNodeLabels": [provider],
                        "LeafNodeType": "It",
                        "State": "passed",
                        "NumAttempts": 1,
                    })
                pathlib.Path(report).write_text(json.dumps([{
                    "SuiteConfig": {"DryRun": False},
                    "SuiteSucceeded": True,
                    "SuiteHasProgrammaticFocus": False,
                    "SpecReports": specs,
                }]), encoding="utf-8")
            sys.exit(int(os.environ.get("FAKE_TEST_EXIT", "0")))
            """,
        )
        shutil.copy(self.bin / "e2e.test", self.root / "e2e" / "e2e.test")
        managed_wrapper = self.root / "hack" / "run-managed-policy-e2e.sh"
        managed_wrapper.parent.mkdir(parents=True, exist_ok=True)
        managed_wrapper.write_text('#!/usr/bin/env bash\nexec "$@"\n', encoding="utf-8")
        managed_wrapper.chmod(0o755)

    def tearDown(self):
        self.temp.cleanup()

    def _write_stub(self, name, source):
        path = self.bin / name
        source = textwrap.dedent(source).lstrip()
        source = source.replace("#!/usr/bin/env python3", f"#!{sys.executable}")
        path.write_text(source, encoding="utf-8")
        path.chmod(0o755)

    def _env(self, **overrides):
        env = os.environ.copy()
        env.update(
            {
                "PATH": os.pathsep.join(
                    (
                        str(self.bin),
                        str(Path(sys.executable).parent),
                        "/usr/bin",
                        "/bin",
                    )
                ),
                "HOME": str(self.home),
                "RUNNER_TEMP": str(self.runner_temp),
                "GITHUB_PATH": str(self.github_path),
                "TEST_TIMEOUT": "1200s",
                "DEVSY_REQUIRE_MICROSANDBOX": "true",
                "GH_USERNAME": "test-owner",
                "GH_ACCESS_TOKEN": "test-token-never-print",
                "GH_CREDENTIAL_USERNAME": "x-access-token",
                "FAKE_SUDO_CAPTURE": str(self.sudo_capture),
                "FAKE_EXPECTED_HASH": EXPECTED_HASH,
                "FAKE_CURL_CAPTURE": str(Path(self.temp.name) / "curl-url"),
            }
        )
        env.update({key: str(value) for key, value in overrides.items()})
        return env

    def _run_script(self, name, *args, **overrides):
        return subprocess.run(
            ["bash", str(self.hack / name), *args],
            cwd=self.root,
            env=self._env(**overrides),
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=30,
            check=False,
        )

    def test_install_uses_pinned_release_and_installs_cached_bundle(self):
        archive = self.runner_temp / "microsandbox-linux-x86_64-v0.7.7.tar.gz"
        archive.write_bytes(b"cached fixture archive")
        result = self._run_script("install-linux.sh")

        self.assertEqual(result.returncode, 0, result.stdout)
        self.assertIn("MicroSandbox v0.7.7", result.stdout)
        self.assertEqual(
            (self.home / ".microsandbox/bin/msb").stat().st_mode & 0o777,
            0o755,
        )
        self.assertTrue((self.home / ".microsandbox/lib/libkrunfw.so").is_symlink())
        self.assertEqual(
            self.github_path.read_text(encoding="utf-8"),
            f"{self.home}/.local/bin\n",
        )
        self.assertFalse(Path(self.temp.name, "curl-url").exists())

    def test_install_checksum_failure_stops_before_extraction(self):
        result = self._run_script("install-linux.sh", FAKE_SHA256_FAIL="1")

        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.home / ".microsandbox/bin/msb").exists())
        self.assertEqual(
            Path(self.temp.name, "curl-url").read_text(encoding="utf-8"),
            RELEASE_URL + "\n",
        )
        self.assertEqual(self.github_path.read_text(encoding="utf-8"), "")

    def test_test_and_tee_failures_keep_original_exit_precedence(self):
        result = self._run_script(
            "run-ci.sh",
            LABEL,
            "2",
            FAKE_TEST_EXIT="23",
            FAKE_TEE_EXIT="47",
        )

        self.assertEqual(result.returncode, 23, result.stdout)
        self.assertIn("fixture E2E output", result.stdout)
        self.assertNotIn("test-token-never-print", result.stdout)
        report = self.runner_temp / f"microsandbox-{LABEL}.json"
        log = self.runner_temp / f"microsandbox-{LABEL}.log"
        self.assertTrue(report.is_file())
        self.assertTrue(log.is_file())
        calls = self.sudo_capture.read_text(encoding="utf-8")
        self.assertTrue(calls.startswith("command=./e2e.test;"))
        self.assertNotIn("DEVSY_REQUIRE_MSB_MANAGED_POLICY_FIXTURE", calls)
        for name in (
            "DEVSY_REQUIRE_MICROSANDBOX",
            "GH_USERNAME",
            "GH_ACCESS_TOKEN",
            "GH_CREDENTIAL_USERNAME",
            "KUBECONFIG",
            "PATH",
        ):
            self.assertIn(name, calls)
        self.assertNotIn("-E", calls)
        self.assertNotIn("test-token-never-print", calls)

    def test_missing_report_fails_report_validation(self):
        result = self._run_script("run-ci.sh", LABEL, "2", FAKE_SKIP_REPORT="1")

        self.assertEqual(result.returncode, 1, result.stdout)
        self.assertIn("cannot read Ginkgo JSON report", result.stdout)
        self.assertTrue((self.runner_temp / f"microsandbox-{LABEL}.log").is_file())

    def test_tee_failure_is_reported_when_test_succeeds(self):
        result = self._run_script("run-ci.sh", LABEL, "2", FAKE_TEE_EXIT="47")

        self.assertEqual(result.returncode, 47, result.stdout)
        self.assertTrue((self.runner_temp / f"microsandbox-{LABEL}.json").is_file())
        self.assertTrue((self.runner_temp / f"microsandbox-{LABEL}.log").is_file())

    def test_test_failure_takes_precedence_over_missing_report(self):
        result = self._run_script(
            "run-ci.sh", LABEL, "2", FAKE_TEST_EXIT="23", FAKE_SKIP_REPORT="1"
        )

        self.assertEqual(result.returncode, 23, result.stdout)
        self.assertIn("cannot read Ginkgo JSON report", result.stdout)
        self.assertTrue((self.runner_temp / f"microsandbox-{LABEL}.log").is_file())

    def test_report_chown_failure_skips_verifier_and_is_report_error(self):
        result = self._run_script("run-ci.sh", LABEL, "2", FAKE_CHOWN_EXIT="43")

        self.assertEqual(result.returncode, 43, result.stdout)
        self.assertIn("could not make Ginkgo report readable", result.stdout)

    def test_managed_policy_label_uses_wrapper_and_forwards_fixture_environment(self):
        result = self._run_script(
            "run-ci.sh",
            "up-provider-microsandbox-managed-recreate",
            "2",
        )

        self.assertEqual(result.returncode, 0, result.stdout)
        call = self.sudo_capture.read_text(encoding="utf-8").splitlines()[0]
        self.assertTrue(call.startswith("command=../hack/run-managed-policy-e2e.sh;"))
        self.assertIn("DEVSY_REQUIRE_MSB_MANAGED_POLICY_FIXTURE", call)
        self.assertIn("RUNNER_TEMP", call)
        self.assertTrue(
            (
                self.runner_temp
                / "microsandbox-up-provider-microsandbox-managed-recreate.json"
            ).is_file()
        )


if __name__ == "__main__":
    unittest.main()
