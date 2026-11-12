---
kind: task
id: devsy-workspace-gc
name: devsy-workspace-gc
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

# Devsy Workspace Garbage Collection

This task identifies and archives completed, merged, or stale automation worktrees in Devsy.
Target project: devsy-org/devsy (Paseo project ID: prj_5e6fbfb713103941).

## Operational Boundaries

* Only inspect and archive workspaces owned by this automation.
* Never archive dirty workspaces or workspaces containing uncommitted changes.
* Never archive developer workspaces or workspaces hosting active playbook runs.
* Never perform long polling. Run a single pass garbage collection check and finish.

## Execution Procedure

### 1. Scan Devsy Workspaces

Query managed worktrees under the Devsy project:

```bash
CURRENT_CWD=$(pwd)
paseo workspace ls --json | jq -r --arg current "$CURRENT_CWD" '.[] | select(.project=="devsy" and .isolation=="worktree" and .cwd != $current) | [.workspaceId, .cwd, .title] | @tsv'
```

### 2. Evaluate Each Workspace Candidate

For each candidate workspace tuple [workspaceId, cwd, title]:

1. Verify automation ownership:
   The workspace title must start with `[devsy]` or its current branch must match `diag/*` or `fix/*`.
   If the workspace is not explicitly owned by this automation, skip it immediately. Never archive developer or unowned worktrees.

2. Check for uncommitted changes:
   Run `git -C "$cwd" status --porcelain`.
   If the worktree is dirty or contains uncommitted changes, never archive it. Record uncommitted changes for the daily digest.

3. Check for running subagents:
   Run:
   ```bash
   RUNNING_AGENTS=$(paseo agent ls --json | jq -r --arg cwd "$cwd" '.[] | select(.cwd==$cwd and (.status=="running" or .status=="initializing")) | .agentId')
   ```
   If running agents are found, skip this workspace.

4. Check for active playbook runs:
   Inspect active playbook runs using the list_runs tool.
   If the candidate workspace matches an active playbook run, or hosts a run in running, waiting_gate, or waiting status, skip this workspace immediately. Never archive a workspace hosting a playbook run waiting at a human gate.

5. Check branch merge and pull request status:
   Run:
   ```bash
   BRANCH=$(git -C "$cwd" rev-parse --abbrev-ref HEAD 2>/dev/null)
   git -C "$cwd" fetch origin main --quiet
   IS_MERGED=$(git -C "$cwd" merge-base --is-ancestor HEAD origin/main && echo "yes" || echo "no")
   PR_STATE=$(gh pr list -R devsy-org/devsy --head "$BRANCH" --state all --json state --jq '.[0].state' 2>/dev/null)
   ```
   If the pull request lookup returns an error or empty result unexpectedly, do not archive. Fail closed.

6. Archive if completed or stale:
   * If IS_MERGED is yes, or PR_STATE is MERGED or CLOSED, and worktree is clean:
     `paseo workspace archive <workspaceId>`
   * If diagnostic branch (`diag/*`) and subagent status is idle or closed:
     If worktree is clean and zero active playbook runs exist, archive:
     `paseo workspace archive <workspaceId>`
   * If older than 48 hours with no uncommitted changes, no open pull request, and zero active playbook runs:
     `paseo workspace archive <workspaceId>`
