#!/usr/bin/env python3
"""Validate that one MicroSandbox Ginkgo label completed its expected specs."""

import argparse
import json
from pathlib import Path
import sys


def _spec_labels(spec):
    labels = spec.get("LeafNodeLabels") or []
    hierarchy = spec.get("ContainerHierarchyLabels") or []
    if not isinstance(labels, list) or any(not isinstance(label, str) for label in labels):
        raise ValueError("spec report has malformed leaf labels")
    if not isinstance(hierarchy, list) or any(
        not isinstance(group, list) or any(not isinstance(label, str) for label in group)
        for group in hierarchy
    ):
        raise ValueError("spec report has malformed container labels")
    result = set(labels)
    for group in hierarchy:
        result.update(group)
    return result


def _read_suite_specs(path):
    try:
        reports = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise ValueError(f"cannot read Ginkgo JSON report: {exc}") from exc

    if not isinstance(reports, list) or len(reports) != 1 or not isinstance(reports[0], dict):
        raise ValueError("expected one Ginkgo suite report")
    suite = reports[0]
    config = suite.get("SuiteConfig")
    if not isinstance(config, dict) or config.get("DryRun") is not False:
        raise ValueError("report does not prove a real test run")
    if suite.get("SuiteSucceeded") is not True:
        raise ValueError("Ginkgo suite did not succeed")
    if suite.get("SuiteHasProgrammaticFocus") is not False:
        raise ValueError("report has programmatic focus")
    specs = suite.get("SpecReports")
    if not isinstance(specs, list):
        raise ValueError("report has no spec list")
    return specs


def _select_specs(specs, expected_label, expected_count):
    if any(not isinstance(spec, dict) for spec in specs):
        raise ValueError("report contains a malformed spec entry")
    selected = [spec for spec in specs if expected_label in _spec_labels(spec)]
    if len(selected) != expected_count:
        raise ValueError(
            f"label {expected_label!r}: expected {expected_count} specs, found {len(selected)}"
        )
    return selected


def _validate_expected_count(expected_count, provider_mode):
    if provider_mode not in ("parity", "external"):
        raise ValueError(f"unsupported provider mode {provider_mode!r}")
    if expected_count <= 0:
        raise ValueError("expected spec count must be positive")
    if provider_mode == "parity" and expected_count % 2:
        raise ValueError("expected spec count must be even in parity mode")


def _validate_selected_specs(selected, expected_count, provider_mode):
    providers = {"microsandbox-builtin": 0, "microsandbox-external": 0}
    for index, spec in enumerate(selected, start=1):
        spec_labels = _spec_labels(spec)
        provider_labels = spec_labels & providers.keys()
        if len(provider_labels) != 1:
            raise ValueError(f"selected spec {index} must have exactly one provider label")
        if provider_mode == "external" and provider_labels != {"microsandbox-external"}:
            raise ValueError(
                f"selected spec {index} must have exactly the microsandbox-external provider label"
            )
        providers[next(iter(provider_labels))] += 1
        if spec.get("LeafNodeType") != "It":
            raise ValueError(f"selected report {index} is not an It spec")
        if spec.get("State") != "passed":
            raise ValueError(f"selected spec {index} state is {spec.get('State')!r}, not passed")
        if spec.get("NumAttempts") != 1:
            raise ValueError(
                f"selected spec {index} used {spec.get('NumAttempts')!r} attempts, expected one"
            )
    if provider_mode == "parity" and any(
        count != expected_count // 2 for count in providers.values()
    ):
        raise ValueError(
            f"provider label counts are {providers}, expected {expected_count // 2} each"
        )
    return len(selected)


def validate_report(path, expected_label, expected_count, provider_mode="parity"):
    _validate_expected_count(expected_count, provider_mode)
    specs = _read_suite_specs(path)
    selected = _select_specs(specs, expected_label, expected_count)
    return _validate_selected_specs(selected, expected_count, provider_mode)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--report", required=True, type=Path)
    parser.add_argument("--label", required=True)
    parser.add_argument("--expected-count", required=True, type=int)
    parser.add_argument("--provider-mode", choices=("parity", "external"), default="parity")
    args = parser.parse_args(argv)
    try:
        _validate_expected_count(args.expected_count, args.provider_mode)
    except ValueError as exc:
        parser.error(str(exc))
    try:
        count = validate_report(args.report, args.label, args.expected_count, args.provider_mode)
    except ValueError as exc:
        print(f"::error::{exc}", file=sys.stderr)
        return 1
    print(f"{args.label}: {count} specs passed in one attempt each")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
