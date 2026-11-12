#!/usr/bin/env python3
import argparse
import json
import subprocess
import os
import sys
from pathlib import Path


def run_command(args: list[str]) -> str:
    try:
        result = subprocess.run(args, capture_output=True, text=True, check=True)
        return result.stdout.strip()
    except subprocess.CalledProcessError as exc:
        err_msg = exc.stderr.strip() if exc.stderr else str(exc)
        sys.stderr.write(f"Command failed ({' '.join(args)}): {err_msg}\n")
        sys.exit(1)

def resolve_repo_root() -> str:
    try:
        common_dir = run_command(["git", "rev-parse", "--git-common-dir"]).strip()
        return os.path.dirname(os.path.abspath(common_dir))
    except Exception:
        return os.getcwd()



def find_schedule_id(schedule_name: str) -> str | None:
    output = run_command(["paseo", "schedule", "ls", "--json"])
    schedules = json.loads(output)
    matching = [s.get("id") for s in schedules if s.get("name") == schedule_name]
    if len(matching) > 1:
        sys.stderr.write(f"Error: Ambiguous duplicate schedules found with name '{schedule_name}': {matching}\n")
        sys.exit(1)
    return matching[0] if matching else None


def handle_diff(config: dict, schedule_name: str, loader_prompt: str) -> None:
    existing_id = find_schedule_id(schedule_name)
    if not existing_id:
        sys.stderr.write(f"Schedule '{schedule_name}' not found in Paseo daemon.\n")
        sys.exit(1)

    inspect_output = run_command(["paseo", "schedule", "inspect", existing_id, "--json"])
    daemon_data = json.loads(inspect_output)

    cadence = daemon_data.get("cadence", {})
    target_config = daemon_data.get("target", {}).get("config", {})

    daemon_provider = target_config.get("provider", "")
    daemon_model = target_config.get("model")
    if daemon_model:
        actual_provider = f"{daemon_provider}/{daemon_model}"
    else:
        actual_provider = daemon_provider

    comparisons = [
        ("cron", config.get("cron"), cadence.get("expression")),
        ("timezone", config.get("timezone"), cadence.get("timezone")),
        ("provider", config.get("provider"), actual_provider),
        ("mode", config.get("mode", "auto"), target_config.get("modeId")),
        ("cwd", config.get("cwd") or resolve_repo_root(), target_config.get("cwd")),
        ("prompt", loader_prompt.strip(), daemon_data.get("prompt", "").strip()),
    ]

    has_drift = False
    print(f"Schedule Configuration Drift Check for '{schedule_name}' ({existing_id}):")
    for field, expected, actual in comparisons:
        if expected != actual:
            has_drift = True
            print(f"  [DRIFT] {field}:")
            print(f"    Expected (Git):    {expected}")
            print(f"    Actual (Daemon):   {actual}")
        else:
            print(f"  [OK]    {field}: {actual}")

    if has_drift:
        print("\nDrift detected. Run with --apply to synchronize Paseo daemon with Git.")
        sys.exit(2)
    else:
        print("\nConfiguration is in sync. Zero drift.")


def handle_apply(config: dict, schedule_name: str, loader_prompt: str) -> None:
    existing_id = find_schedule_id(schedule_name)
    if not existing_id:
        sys.stderr.write(f"Schedule '{schedule_name}' not found. Run --create first.\n")
        sys.exit(1)

    cmd = [
        "paseo",
        "schedule",
        "update",
        existing_id,
        "--cron",
        config["cron"],
        "--timezone",
        config["timezone"],
        "--provider",
        config["provider"],
        "--mode",
        config.get("mode", "auto"),
        "--cwd",
        config.get("cwd") or resolve_repo_root(),
        "--prompt",
        loader_prompt,
    ]
    run_command(cmd)

    inspect_output = run_command(["paseo", "schedule", "inspect", existing_id, "--json"])
    updated_data = json.loads(inspect_output)
    cadence = updated_data.get("cadence", {})
    print(f"Updated schedule {existing_id} successfully.")
    print(f"Verified daemon cron: {cadence.get('expression')} ({cadence.get('timezone')})")


def handle_create(config: dict, schedule_name: str, loader_prompt: str) -> None:
    existing_id = find_schedule_id(schedule_name)
    if existing_id:
        sys.stderr.write(f"Schedule '{schedule_name}' already exists with ID {existing_id}. Use --apply to update.\n")
        sys.exit(1)

    cmd = [
        "paseo",
        "schedule",
        "create",
        "--name",
        schedule_name,
        "--cron",
        config["cron"],
        "--timezone",
        config["timezone"],
        "--provider",
        config["provider"],
        "--mode",
        config.get("mode", "auto"),
        "--cwd",
        config.get("cwd") or resolve_repo_root(),
        loader_prompt,
    ]
    output = run_command(cmd)
    print(f"Created schedule: {output}")


def main() -> None:
    parser = argparse.ArgumentParser(description="Synchronize Paseo schedule with Git configuration")
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--diff", action="store_true", help="Compare Paseo daemon schedule against Git configuration")
    group.add_argument("--apply", action="store_true", help="Update Paseo daemon schedule with Git configuration")
    group.add_argument("--create", action="store_true", help="Register schedule in Paseo daemon for the first time")

    args = parser.parse_args()

    base_dir = Path(__file__).resolve().parent
    config_file = base_dir / "schedule.json"
    loader_file = base_dir / "loader.txt"

    if not config_file.exists():
        sys.stderr.write(f"Config file not found: {config_file}\n")
        sys.exit(1)
    if not loader_file.exists():
        sys.stderr.write(f"Loader file not found: {loader_file}\n")
        sys.exit(1)

    with open(config_file, "r") as f:
        config = json.load(f)

    with open(loader_file, "r") as f:
        loader_prompt = f.read().strip()

    schedule_name = config.get("name", "devsy-daily-triage")

    if args.diff:
        handle_diff(config, schedule_name, loader_prompt)
    elif args.apply:
        handle_apply(config, schedule_name, loader_prompt)
    elif args.create:
        handle_create(config, schedule_name, loader_prompt)


if __name__ == "__main__":
    main()
