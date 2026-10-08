"""Publish Go test durations without retaining test output or fixture contents."""

import argparse
import json
from pathlib import Path
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("input", type=Path)
    parser.add_argument("artifact", type=Path)
    parser.add_argument("summary", type=Path)
    parser.add_argument("--required-test", required=True)
    parser.add_argument("--count", type=int, default=1)
    args = parser.parse_args()

    package = "github.com/devsy-org/devsy/pkg/secrets"
    events = []
    with args.input.open() as source:
        for line in source:
            event = json.loads(line)
            if event.get("Package") == package and event.get("Action") in {"pass", "fail", "skip"}:
                events.append({key: event[key] for key in ("Action", "Package", "Test", "Elapsed") if key in event})

    args.artifact.write_text("".join(json.dumps(event) + "\n" for event in events))
    required_passes = sum(
        event["Action"] == "pass" and event.get("Test") == args.required_test
        for event in events
    )
    package_passed = any(event["Action"] == "pass" and "Test" not in event for event in events)
    failures = [event for event in events if event["Action"] == "fail"]
    passed_tests = sorted(
        (event for event in events if event["Action"] == "pass" and "Test" in event),
        key=lambda event: event.get("Elapsed", 0),
        reverse=True,
    )
    with args.summary.open("a") as summary:
        summary.write("## Secrets test durations\n\n")
        summary.write(f"Required test passes: {required_passes}/{args.count}.\n\n")
        for event in events:
            if "Test" not in event:
                summary.write(f"Package result: {event['Action']}; elapsed: {event.get('Elapsed', 0):.3f}s.\n\n")
        summary.write("| Slowest test executions | Seconds |\n| --- | ---: |\n")
        for event in passed_tests[:10]:
            name = event["Test"].replace("|", "&#124;").replace("\n", " ")
            summary.write(f"| {name} | {event.get('Elapsed', 0):.3f} |\n")
        for event in failures:
            summary.write(f"\nFailed: {event.get('Test', package)}\n")

    if not package_passed or failures or required_passes != args.count:
        sys.exit("Secrets test validation failed: require a passing package and all expected test executions")


if __name__ == "__main__":
    main()
