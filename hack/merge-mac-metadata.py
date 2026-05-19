#!/usr/bin/env python3
"""Merge macOS electron-updater metadata from separate arch builds.

When building macOS arm64 and x64 separately, each produces its own
latest-mac.yml (or beta-mac.yml) with only one architecture's files.
This script merges them into a single file with entries for both arches.

Usage: python3 merge-mac-metadata.py <metadata-dir> <output-dir>
"""

import glob
import sys
from pathlib import Path

import yaml


def merge_mac_files(metadata_dir: str, output_dir: str) -> None:
    metadata_path = Path(metadata_dir)
    output_path = Path(output_dir)

    for prefix in ("latest-mac", "beta-mac"):
        pattern = str(metadata_path / "**" / f"{prefix}.yml")
        found = glob.glob(pattern, recursive=True)
        if not found:
            continue

        merged_files = []
        base_data = None

        for filepath in found:
            with open(filepath) as f:
                data = yaml.safe_load(f)
            if data is None:
                continue
            if base_data is None:
                base_data = data
            if "files" in data:
                merged_files.extend(data["files"])

        if base_data is None:
            continue

        base_data["files"] = merged_files

        # Set path to the first file entry (electron-updater uses this as default)
        if merged_files:
            base_data["path"] = merged_files[0]["url"]
            base_data["sha512"] = merged_files[0]["sha512"]
            base_data["size"] = merged_files[0].get("size")

        out_file = output_path / f"{prefix}.yml"
        with open(out_file, "w") as f:
            yaml.dump(base_data, f, default_flow_style=False, sort_keys=False)

        print(f"Merged {len(found)} files into {out_file}")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        print(f"Usage: {sys.argv[0]} <metadata-dir> <output-dir>", file=sys.stderr)
        sys.exit(1)
    merge_mac_files(sys.argv[1], sys.argv[2])
