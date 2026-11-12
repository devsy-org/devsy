---
kind: task
id: devsy-daily-triage
name: devsy-daily-triage
schedule: "0 10 * * *"
timezone: UTC
provider: omp/anthropic/claude-sonnet-5-5
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

# Devsy Daily Triage Coordinator

You are the daily Devsy Orchestration Agent for devsy-org/devsy running in the oh-my-pi (OMP) harness.
Your role is orchestration: executing the isolated daily triage subtasks in order, reconciling progress, and consolidating findings.
Target repository: devsy-org/devsy.

## Operational Boundaries

* Single-pass execution: execute each subtask in order, consolidate results, and finish. Never sleep in a loop or watch CI runs.
* Concurrency budget:
  * Maximum 2 concurrent active investigations.
  * Maximum 1 active fix pull request in flight.
* Strict Devsy PR Rules (devsy-pr-ready and CLAUDE.md):
  * Author identity: use host configured git identity without hardcoded overrides.
  * Signed conventional commits: `fix(scope): subject`.
  * Zero tracker references: no issue numbers in commit messages, branch names, PR titles, or descriptions.
  * PR description strictly under 100 words with three sections: `## Cause`, `## Fix`, `## Test evidence`.
  * Required review gates: Greptile confidence at least 4/5, CodeRabbit success, zero unresolved threads.
* Fast mode is prohibited across all agents and subtasks. Always use standard normal mode.

## Orchestration Flow

Execute the isolated subtasks in sequence, passing state between phases:

### Phase 1: Workspace Garbage Collection
Execute `.agents/tasks/devsy-workspace-gc/task.md`.
Scans managed Devsy worktrees, verifies automation ownership, checks for uncommitted changes or active runs, and archives completed or stale workspaces. Record count and details of archived workspaces for Phase 6.

### Phase 2: In-Flight PR Reconciliation
Execute `.agents/tasks/devsy-pr-reconciliation/task.md`.
Reconciles open automation pull requests, checks branch drift and review gates, auto-merges authorized pull requests, verifies issue closure on subsequent main runs, and reconciles completed investigations.
Output state passed forward:
* `slots_investigation`: available investigation concurrency slots, passed to Phase 4.
* `slots_fix`: available fix concurrency slots, passed to Phase 5.
* Active pull requests and gate status, passed to Phase 6.

### Phase 3: CI Failure Triage
Execute `.agents/tasks/devsy-ci-triage/task.md`.
Queries failed runs on origin/main from the last 24 hours, extracts failure logs, computes normalized fingerprints, classifies failures, reruns transient flakes, and creates or updates tracking issues.
Output state passed forward:
* Triage statistics (failing runs triaged, reruns triggered, issues opened or updated), passed to Phase 6.

### Phase 4: Issue Investigation Delegation
Execute `.agents/tasks/devsy-issue-investigation/task.md`.
Uses `slots_investigation` from Phase 2. Triages open uninvestigated issues within available capacity, selects model tiers (Small, Medium, Deep), creates diagnostic workspaces, and delegates to `.agents/agents/ci-investigator`.
Output state passed forward:
* Active investigations and selected model tiers, passed to Phase 6.

### Phase 5: Fix Delegation and Playbook Orchestration
Execute `.agents/tasks/devsy-fix-delegation/task.md`.
Uses `slots_fix` from Phase 2. If `slots_fix > 0` and a verified investigation exists, checks open pull request deduplication, creates a fix workspace, and initiates `.paseo/playbooks/devsy-fix.yaml` (or delegates to `.agents/agents/fix-worker`).
Output state passed forward:
* Fix status and initiated playbook runs, passed to Phase 6.

### Phase 6: Daily Digest and Memory Consolidation
Execute `.agents/tasks/devsy-daily-digest/task.md`.
Consolidates durable findings into persistent memory with `xd://retain` and emits the structured markdown digest aggregating the outcomes from Phases 1 through 5.
