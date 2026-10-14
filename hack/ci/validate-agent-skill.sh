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
"$python_bin" -c 'import sys; assert sys.version_info >= (3, 11), "Python 3.11+ is required (set PYTHON to its executable)"'

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT
export DISABLE_TELEMETRY=1
export NO_COLOR=1

"$python_bin" -m venv "$tmp_dir/validator"
"$tmp_dir/validator/bin/python" -m pip install --quiet --disable-pip-version-check \
    "skills-ref @ git+https://github.com/agentskills/agentskills.git@${skills_ref_commit}#subdirectory=skills-ref"
"$tmp_dir/validator/bin/skills-ref" validate "$skill_dir"

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
