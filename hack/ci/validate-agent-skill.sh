#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
skill_dir="$repo_root/.agents/skills/devsy"
skills_version="1.7.0"
skills_ref_commit="69ef37e9424c0a7ea9dd2293b559e43ec8176379"
python_bin="${PYTHON:-python3}"

for file in SKILL.md references/{mcp-tools,cli,workspace-workflows,providers,safety,troubleshooting}.md; do
    test -s "$skill_dir/$file" || {
        echo "Missing skill file: $file" >&2
        exit 1
    }
done
if [[ -e "$repo_root/skills/devsy" || -L "$repo_root/skills/devsy" ]]; then
    echo "Duplicate skill source at skills/devsy" >&2
    exit 1
fi
"$python_bin" -c 'import sys; assert sys.version_info >= (3, 11), "Python 3.11+ is required (set PYTHON to its executable)"'

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
export DISABLE_TELEMETRY=1
export NO_COLOR=1

"$python_bin" -m venv "$tmp_dir/validator"
"$tmp_dir/validator/bin/python" -m pip install --quiet --disable-pip-version-check \
    "skills-ref @ git+https://github.com/agentskills/agentskills.git@${skills_ref_commit}#subdirectory=skills-ref"
"$tmp_dir/validator/bin/skills-ref" validate "$skill_dir"

"$tmp_dir/validator/bin/python" - "$skill_dir" <<'PY'
import re
import sys
from pathlib import Path

from skills_ref.parser import parse_frontmatter

skill = Path(sys.argv[1])
source = (skill / "SKILL.md").read_text()
metadata, _ = parse_frontmatter(source)
assert metadata["name"] == "devsy", "Expected name: devsy"
assert "allowed-tools" not in metadata, "Do not bind the skill to client tool permissions"
assert all(isinstance(value, str) for value in metadata.get("metadata", {}).values())
assert len(source.splitlines()) < 500, "Keep the entrypoint below 500 lines"
for reference in skill.joinpath("references").glob("*.md"):
    assert f"(references/{reference.name})" in source, f"Unlinked reference: {reference.name}"
for document in [skill / "SKILL.md", *skill.joinpath("references").glob("*.md")]:
    for target in re.findall(r"\]\(([^)]+)\)", document.read_text()):
        if "://" in target or target.startswith("#"):
            continue
        resolved = (document.parent / target.split("#", 1)[0]).resolve()
        assert resolved.is_relative_to(skill.resolve()), f"Reference escapes skill: {target}"
        assert resolved.is_file(), f"Broken reference: {target}"
PY

npx --yes "skills@$skills_version" add "$repo_root" --list >"$tmp_dir/discovery.txt"
"$tmp_dir/validator/bin/python" - "$tmp_dir/discovery.txt" <<'PYTHON'
import re
import sys
from pathlib import Path

output = Path(sys.argv[1]).read_text()
output = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", output)
assert re.search(r"Found 1 skill\b", output), f"Expected exactly one skill:\n{output}"
names = re.findall(r"^│    ([a-z0-9-]+)\s*$", output, re.MULTILINE)
assert names == ["devsy"], f"Expected only the devsy skill, discovered: {names}"
PYTHON

for agent in codex claude-code; do
    install_dir="$tmp_dir/$agent"
    mkdir -p "$install_dir"
    (
        cd "$install_dir"
        npx --yes "skills@$skills_version" add "$repo_root" --skill devsy \
            --agent "$agent" --copy --yes --json >"$tmp_dir/$agent.json"
    )
    case "$agent" in
        codex) installed="$install_dir/.agents/skills/devsy" ;;
        claude-code) installed="$install_dir/.claude/skills/devsy" ;;
    esac
    test -f "$installed/SKILL.md"
    diff -r "$skill_dir" "$installed"
done

echo "Devsy skill conformance, discovery, and Codex/Claude Code installation passed."
