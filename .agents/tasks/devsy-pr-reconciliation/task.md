---
kind: task
id: devsy-pr-reconciliation
name: devsy-pr-reconciliation
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

# Devsy In-Flight Pull Request Reconciliation

This task reconciles open pull requests created by this automation, checks review gates, verifies issue resolution, and reconciles completed investigations.
Target repository: devsy-org/devsy.

## Task Inputs & Defaults

* `auto_merge`: boolean, default `false`. Auto-merge requires explicit caller or human authorization.

## Operational Boundaries

* Concurrency budget:
  * Maximum 2 concurrent active investigations.
  * Maximum 1 active fix pull request in flight.
* Review gates required before merge:
  * CI status checks passing.
  * Greptile confidence score at least 4 out of 5.
  * CodeRabbit review passing with zero unresolved threads.
  * Signed conventional commit.
  * Zero tracker references in commit message, branch name, title, or body.
  * Description under 100 words in three exact sections.

## Execution Procedure

### 1. List Open Automation Pull Requests

Query open pull requests authored by this automation:

```bash
gh pr list -R devsy-org/devsy --state open --author @me --json number,title,headRefName,statusCheckRollup,isDraft,mergeStateStatus,mergeable
```

Filter for branches matching `fix/*` created by this automation. Skip any pull request not explicitly owned by this automation.

### 2. Advance Open Pull Requests

For each open automation pull request:

1. Check branch drift and merge conflicts:
   Inspect `mergeStateStatus`.
   If BEHIND, steer the worker to rebase onto origin/main, rerun local validation, and push with `--force-with-lease`. Limit to one rebase attempt per day.
   If DIRTY, steer the worker once to resolve conflicts. If still conflicting on the next run, label the pull request blocked:
   `gh pr edit <pr_number> -R devsy-org/devsy --add-label blocked`
   Never merge a pull request whose base branch is stale.

2. Handle CI failures:
   Inspect failing status checks. If a transient infrastructure failure occurred, rerun the failed job:
   `gh run rerun <run_id> --failed`
   If a code failure occurred, steer the worker agent to diagnose and correct it.

3. Promote draft pull requests to ready:
   If all CI checks pass on the current head commit SHA and the pull request is draft, mark it ready:
   `gh pr ready <pr_number> -R devsy-org/devsy`

4. Review thread handling:
   Inspect CodeRabbit and Greptile reviews on ready pull requests. Check for unresolved review threads:
   ```bash
   gh api graphql -f query='query { repository(owner:"devsy-org", name:"devsy") { pullRequest(number:<pr_number>) { reviewThreads(first:50) { nodes { isResolved } } } } }' --jq '[.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved==false)] | length'
   ```
   Steer the worker agent if unresolved comments or review findings remain.

5. Auto-merge execution and gate verification:
   Auto-merge runs only when the coordinator input explicitly sets `auto_merge` to true.
   When all gates pass (CI green, Greptile at least 4/5, CodeRabbit pass, 0 open threads, signed commit, 0 tracker references, body under 100 words), squash merge:
   `gh pr merge <pr_number> -R devsy-org/devsy --squash --delete-branch --subject "<title>" --body ""`
   Then archive its workspace: `paseo workspace archive <workspace_id>`.
   When auto-merge is not enabled, record ready pull requests for human review.

6. Issue closure and post-merge verification:
   After a fix pull request merges, look up the fix workspace label issue.
   Keep the issue open until the failing job is confirmed green on a subsequent run on main.
   Close the issue with a comment containing the verified passing run ID and head SHA.
   Retain resolution fact in persistent memory:
   Write to `xd://retain` with:
   `{"facts": [{"fact": "devsy ci fingerprint <fingerprint>: resolved by commit <sha>", "scope": "project"}]}`

### 3. Reconcile In-Flight Investigations

1. For completed investigation subagents, read final analysis.
2. If root cause is verified:
   Retain finding in persistent memory:
   Write to `xd://retain` with:
   `{"facts": [{"fact": "devsy root cause for <investigation_subject>: <summary>", "scope": "project"}]}`
   Archive diagnostic workspace: `paseo workspace archive <workspace_id>`.
   If fix budget allows (0 active fix pull requests), promote to fix task.

### 4. Calculate Available Capacity

Compute operational slots for downstream tasks:
* `slots_investigation`: calculate `max(0, 2 - active_investigations_count)`.
* `slots_fix`: calculate `max(0, 1 - active_fix_prs_count)`.
