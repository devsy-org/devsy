import json
from pathlib import Path
import tempfile
import unittest

from verify_report import validate_report


LABEL = "up-provider-microsandbox-images"
PROVIDERS = ("microsandbox-builtin", "microsandbox-external")


def report(count=2):
    return [{
        "SuiteConfig": {"DryRun": False},
        "SuiteSucceeded": True,
        "SuiteHasProgrammaticFocus": False,
        "SpecReports": [
            {
                "ContainerHierarchyLabels": [[LABEL]],
                "LeafNodeLabels": [PROVIDERS[index % 2]],
                "LeafNodeType": "It",
                "State": "passed",
                "NumAttempts": 1,
            }
            for index in range(count)
        ],
    }]


class VerifyReportTest(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.path = Path(self.directory.name) / "report.json"

    def tearDown(self):
        self.directory.cleanup()

    def write(self, value):
        self.path.write_text(json.dumps(value), encoding="utf-8")

    def test_accepts_exact_successful_label_count(self):
        self.write(report())
        self.assertEqual(validate_report(self.path, LABEL, 2), 2)

    def test_rejects_missing_report(self):
        with self.assertRaisesRegex(ValueError, "cannot read"):
            validate_report(self.path, LABEL, 2)

    def test_rejects_malformed_report(self):
        self.path.write_text("{", encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "cannot read"):
            validate_report(self.path, LABEL, 2)

    def test_rejects_skipped_or_pending_spec(self):
        for state in ("skipped", "pending"):
            data = report()
            data[0]["SpecReports"][0]["State"] = state
            self.write(data)
            with self.subTest(state=state), self.assertRaisesRegex(ValueError, "not passed"):
                validate_report(self.path, LABEL, 2)

    def test_rejects_failed_suite_or_spec(self):
        data = report()
        data[0]["SuiteSucceeded"] = False
        self.write(data)
        with self.assertRaisesRegex(ValueError, "suite did not succeed"):
            validate_report(self.path, LABEL, 2)
        data = report()
        data[0]["SpecReports"][0]["State"] = "failed"
        self.write(data)
        with self.assertRaisesRegex(ValueError, "not passed"):
            validate_report(self.path, LABEL, 2)

    def test_rejects_dry_run(self):
        data = report()
        data[0]["SuiteConfig"]["DryRun"] = True
        self.write(data)
        with self.assertRaisesRegex(ValueError, "real test run"):
            validate_report(self.path, LABEL, 2)

    def test_rejects_programmatic_focus(self):
        data = report()
        data[0]["SuiteHasProgrammaticFocus"] = True
        self.write(data)
        with self.assertRaisesRegex(ValueError, "programmatic focus"):
            validate_report(self.path, LABEL, 2)

    def test_rejects_retry(self):
        data = report()
        data[0]["SpecReports"][1]["NumAttempts"] = 2
        self.write(data)
        with self.assertRaisesRegex(ValueError, "expected one"):
            validate_report(self.path, LABEL, 2)

    def test_rejects_wrong_label(self):
        self.write(report())
        with self.assertRaisesRegex(ValueError, "found 0"):
            validate_report(self.path, "up-provider-microsandbox-storage", 2)

    def test_rejects_wrong_count(self):
        self.write(report())
        with self.assertRaisesRegex(ValueError, "expected 3 specs, found 2"):
            validate_report(self.path, LABEL, 3)

    def test_rejects_missing_provider_coverage(self):
        data = report()
        data[0]["SpecReports"][1]["LeafNodeLabels"] = [PROVIDERS[0]]
        self.write(data)
        with self.assertRaisesRegex(ValueError, "provider label counts"):
            validate_report(self.path, LABEL, 2)

    def test_rejects_ambiguous_or_missing_provider_label(self):
        for labels in ([], list(PROVIDERS)):
            data = report()
            data[0]["SpecReports"][0]["LeafNodeLabels"] = labels
            self.write(data)
            with self.subTest(labels=labels), self.assertRaisesRegex(
                ValueError, "exactly one provider label"
            ):
                validate_report(self.path, LABEL, 2)


if __name__ == "__main__":
    unittest.main()
