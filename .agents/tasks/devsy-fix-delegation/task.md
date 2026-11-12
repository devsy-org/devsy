---
kind: task
id: devsy-fix-delegation
name: devsy-fix-delegation
target: paseo
cwd: /Users/ocean_trader/Documents/devsy/devsy
isolation: worktree
fast_mode_allowed: false
models_allowed:
  - claude/claude-haiku-5-5
  - codex/gpt-6-luna
  - codex/gpt-6.1-terra
  - claude/claude-sonnet-5-5
  - codex/gpt-6.1-sol
  - antigravity/gemini-3.8-flash
---

# Devsy Fix Delegation and Playbook Orchestration

This task orchestrates bug fix implementation for verified issues, enforcing open pull request deduplication, human approval gates, and PR readiness standards.
Target project: devsy-org/devsy (Paseo project ID: prj_5e6fbfb713103941).

## Operational Boundaries

* Execute only if `slots_fix > 0` and a verified investigation exists.
* Strictly enforce Devsy PR rules (devsy-pr-ready and CLAUDE.md):
  * Use host configured git identity without hardcoded overrides.
  * Signed conventional commit: `fix(scope): subject`.
  * Zero tracker references in commit messages, branch names, PR titles, or bodies.
  * PR description strictly under 100 words with three sections: `## Cause`, `## Fix`, `## Test evidence`.
* Fast mode is prohibited. Subagents must run strictly in normal mode.

## Execution Procedure

### 1. Open Pull Request Deduplication

Before creating a fix workspace or starting the fix pipeline, verify that no open pull request already targets this failure:

```bash
gh pr list -R devsy-org/devsy --state open --search "<fingerprint>"
```

If an open pull request already exists, do not launch a duplicate fix. Record the active pull request for the daily digest and finish.

### 2. Authoritative Execution Path: Paseo Playbook

The authoritative path for executing code fixes is the declarative Paseo Playbook at `.paseo/playbooks/devsy-fix.yaml`. This ensures that human review gates (`approve_plan`, `approve_pr`) and project-wide concurrency (`concurrency: 1`) are strictly enforced by the playbook engine.

1. Setup fix workspace:
   ```bash
   paseo workspace create --isolation worktree --mode branch-off --base origin/main --new-branch fix/<short_topic> --json
   ```

2. Start Playbook Run:
   Initiate `.paseo/playbooks/devsy-fix.yaml` in the newly created fix workspace with inputs:
   * `fingerprint`: `<fingerprint>`
   * `description`: `<root_cause_summary>`
   * `failing_step`: `<step_name>`

### 3. Alternative Direct Worker Subagent (Normal Mode Only)

If executing via direct subagent profile:

1. Delegate to `.agents/agents/fix-worker/agent.md` using `.agents/agents/fix-worker/config.json`.
2. Authorized provider configurations:
   * `codex/gpt-6.1-sol`
   * `codex/gpt-6.1-terra`
   * `claude/claude-sonnet-5-5`
3. Worker instructions adhering strictly to devsy-pr-ready:
   ```markdown
   You are fixing a verified issue in devsy-org/devsy.
   Follow devsy-pr-ready strictly:
   1. Minimal behavior-preserving fix and targeted regression test.
   2. Write self-documenting code with plain ASCII; no redundant comments.
   3. Local validation:
      mkdir -p dist
      mise run cli:format
      mise run cli:lint:ci
      mise run cli:test
   4. Commit:
      Signed conventional commit: git commit -S -m 'fix(<scope>): <short description>'
      Author: use host configured git identity.
      Zero tracker references in commit or PR.
   5. CodeRabbit CLI review on committed change:
      coderabbit review --agent --committed --base-commit $(git merge-base HEAD origin/main)
      If actionable findings exist, fix them, revalidate, and amend:
      git commit -S --amend --no-edit
   6. Push and Create Draft PR using this template:
      gh pr create -R devsy-org/devsy --draft \
        --title "fix(<scope>): <short description>" \
        --body "$(cat <<'EOF'
   ## Cause
   <concise cause description under 30 words>

   ## Fix
   <concise fix description under 30 words>

   ## Test evidence
   <validation commands run and test output summary>
   devsy-fingerprint: <fingerprint>
   EOF
   )"
   (PR body must be strictly under 100 words total with no other sections).
   ```
