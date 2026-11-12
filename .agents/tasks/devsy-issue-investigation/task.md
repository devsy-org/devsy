---
kind: task
id: devsy-issue-investigation
name: devsy-issue-investigation
target: paseo
isolation: worktree
fast_mode_allowed: false
models_allowed:
  - claude/claude-haiku-5-5
  - codex/gpt-6-luna
  - codex/gpt-6.1-terra
  - claude/claude-sonnet-5-5
  - codex/gpt-6.1-sol
  - antigravity/gemini-3.8-flash-medium
---

# Devsy Issue Investigation Delegation

This task triages open issues, tags feature requests and already investigated issues, selects priority failure candidates within capacity, and delegates read-only diagnostic investigations to subagents using `.agents/agents/ci-investigator`.
Target repository: devsy-org/devsy.

## Task Inputs & Defaults

* `slots_investigation`: integer. If not supplied, compute available capacity directly from active subagents:
  `ACTIVE_COUNT=$(paseo agent ls --json 2>/dev/null | jq '[.[] | select(.labels.tier=="investigation" and (.status=="running" or .status=="initializing"))] | length')`
  `slots_investigation=$(( 2 - ACTIVE_COUNT > 0 ? 2 - ACTIVE_COUNT : 0 ))`

## Operational Boundaries

* Concurrency limit: up to `slots_investigation` concurrent active investigations (maximum 2 total).
* Strictly read-only investigation: subagents never edit, commit, or push code.
* Strictly standard normal mode: fast mode is prohibited across all subagents.
* Target branch: origin/main.

## Execution Procedure

### 1. Query Open Uninvestigated Issues

List recent open issues:

```bash
gh issue list -R devsy-org/devsy --state open --limit 20 --json number,title,labels
```

### 2. Triage and Tag

Review issues for preliminary classification:
* Feature requests: ensure label exists and tag issue:
  `gh label create enhancement -R devsy-org/devsy 2>/dev/null || true`
  `gh issue edit <issue_id> -R devsy-org/devsy --add-label enhancement`
* Already investigated issues: ensure label exists and tag issue:
  `gh label create investigated -R devsy-org/devsy 2>/dev/null || true`
  `gh issue edit <issue_id> -R devsy-org/devsy --add-label investigated`

### 3. Select Candidate Issues

Select up to `slots_investigation` candidate issues.
Prioritize CI failures on main first, followed by high-impact bugs.

### 4. Select Model Tier for Investigation (Normal Mode Only)

Select the model configuration according to failure complexity:

* Small Tier (single package, CLI flag or typo, simple unit assertion):
  provider: `claude/claude-haiku-5-5`, `codex/gpt-6-luna`, or `antigravity/gemini-3.8-flash-medium`
* Medium Tier (multi-package interaction, provider parameters, state race):
  provider: `codex/gpt-6.1-terra` or `claude/claude-sonnet-5-5` (standard reasoning)
* Deep Tier (daemon lifecycle, container idle timeouts, k8s pod reconciliation, SSH tunneling):
  provider: `codex/gpt-6.1-sol` or `claude/claude-sonnet-5-5` (high reasoning)

### 5. Launch Investigation Subagent

For each selected candidate issue:

1. Create an isolated diagnostic workspace:
   ```bash
   paseo workspace create --isolation worktree --mode branch-off --base origin/main --new-branch diag/<fingerprint> --json
   ```

2. Delegate to the subagent profile defined at `.agents/agents/ci-investigator/agent.md` using configuration `.agents/agents/ci-investigator/config.json`.

3. Format the subagent payload:
   ```json
   {
     "workspaceId": "<workspace_id>",
     "provider": "<selected_provider_and_model>",
     "title": "[devsy] Investigate <fingerprint>",
     "labels": { "repo": "devsy", "tier": "investigation", "fingerprint": "<fingerprint>" },
     "agentProfile": ".agents/agents/ci-investigator/agent.md",
     "initialPrompt": "# Investigation Brief for Issue <issue_id>\n\n## Context\nFailure fingerprint: <fingerprint>\nIssue description: <issue_description>\n\n## Objectives\n1. Identify root cause with exact file paths and line numbers.\n2. Formulate minimal reproduction command or test case.\n3. Propose minimal fix hypothesis adhering to devsy-pr-ready.\n\n## Constraints\n* Strictly read-only: do not commit, push, or edit repository files.\n* Format final report using the output template."
   }
   ```
