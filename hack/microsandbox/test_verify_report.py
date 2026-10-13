import json
from pathlib import Path
import tempfile
import unittest

from verify_report import validate_report


LABEL = "up-provider-microsandbox-images"
PROVIDERS = ("microsandbox-builtin", "microsandbox-external")


def report(count=2, provider_labels=None):
    if provider_labels is None:
        provider_labels = [PROVIDERS[index % 2] for index in range(count)]
    return [{
        "SuiteConfig": {"DryRun": False},
        "SuiteSucceeded": True,
        "SuiteHasProgrammaticFocus": False,
        "SpecReports": [
            {
                "ContainerHierarchyLabels": [[LABEL]],
                "LeafNodeLabels": [provider_labels[index]],
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

    def test_accepts_odd_external_only_count(self):
        self.write(report(3, [PROVIDERS[1]] * 3))
        self.assertEqual(validate_report(self.path, LABEL, 3, provider_mode="external"), 3)

    def test_external_mode_rejects_builtin_or_mixed_provider_labels(self):
        cases = (
            (
                "builtin",
                [[PROVIDERS[0]]] * 3,
                "exactly the microsandbox-external",
            ),
            (
                "mixed",
                [[PROVIDERS[1]], [PROVIDERS[0]], [PROVIDERS[1]]],
                "exactly the microsandbox-external",
            ),
            (
                "ambiguous",
                [list(PROVIDERS), [PROVIDERS[1]], [PROVIDERS[1]]],
                "exactly one provider label",
            ),
        )
        for name, labels, message in cases:
            data = report(3)
            for spec, provider_labels in zip(data[0]["SpecReports"], labels):
                spec["LeafNodeLabels"] = provider_labels
            self.write(data)
            with self.subTest(name=name), self.assertRaisesRegex(ValueError, message):
                validate_report(self.path, LABEL, 3, provider_mode="external")

    def test_external_mode_rejects_skipped_or_retried_specs(self):
        for field, value, message in (
            ("State", "skipped", "not passed"),
            ("NumAttempts", 2, "expected one"),
        ):
            data = report(3, [PROVIDERS[1]] * 3)
            data[0]["SpecReports"][1][field] = value
            self.write(data)
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, message):
                validate_report(self.path, LABEL, 3, provider_mode="external")

    def test_provider_mode_and_expected_count_are_validated(self):
        self.write(report(1, [PROVIDERS[1]]))
        with self.assertRaisesRegex(ValueError, "unsupported provider mode"):
            validate_report(self.path, LABEL, 1, provider_mode="unknown")
        with self.assertRaisesRegex(ValueError, "must be positive"):
            validate_report(self.path, LABEL, 0, provider_mode="external")
        with self.assertRaisesRegex(ValueError, "must be even in parity mode"):
            validate_report(self.path, LABEL, 1)

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
        with self.assertRaisesRegex(ValueError, "expected 4 specs, found 2"):
            validate_report(self.path, LABEL, 4)

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
