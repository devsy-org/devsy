---
kind: task
id: devsy-ci-triage
name: devsy-ci-triage
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

# Devsy CI Failure Triage

This task inspects failed GitHub Actions workflow runs on origin/main, classifies failures, deduplicates issues, triggers safe reruns for transient flakes, and files structured tracking issues.
Target repository: devsy-org/devsy.

## Operational Boundaries

* Target branch: origin/main.
* Use memory only for durable knowledge: normalized failure fingerprints and flake classifications.
* The live GitHub environment is the sole authority for active issues and runs.
* Do not perform long polling or loops. Execute a single triage pass and finish.

## Execution Procedure

### 1. Query Failed Workflow Runs on Main

Query failed runs from the last 24 hours:

```bash
SINCE=$(date -u -v-24H "+%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || date -u -d "24 hours ago" "+%Y-%m-%dT%H:%M:%SZ")
gh run list -R devsy-org/devsy --branch main --created ">=$SINCE" --status failure --limit 50 --json databaseId,workflowName,headSha,conclusion
```

### 2. Inspect Failed Jobs

For each failed run, inspect failed jobs:

```bash
gh api repos/devsy-org/devsy/actions/runs/<run_id>/jobs --paginate --jq '.jobs[] | select(.conclusion=="failure") | {job_name: .name, job_id: .id, run_attempt: .run_attempt, failed_step: (.steps[]? | select(.conclusion=="failure") | .name)}'
```

### 3. Extract Failure Signature with Bounded Log Extraction

Fetch the trailing failure log:

```bash
gh run view -R devsy-org/devsy --job <job_id> --log-failed | tail -n 150
```

Compute the standardized failure fingerprint using a normalized pipeline:
```bash
SIGNATURE=$(printf '%s|%s|%s|%s' "$WORKFLOW" "$JOB" "$STEP" "$TEST" | sed -E 's/\x1B\[[0-9;]*[a-zA-Z]//g; s/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z//g; s/0x[0-9a-fA-F]+//g' | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')
FINGERPRINT=$(printf '%s' "$SIGNATURE" | shasum -a 256 | cut -c1-12)
```
The fingerprint is the first 12 hex characters of SHA-256 over the lowercase string `workflow|job|step|test`, after stripping ANSI escape sequences, timestamps, and hex memory addresses.
### 4. Classify and Deduplicate

Evaluate the extracted signature against these classifications:

1. Upstream provider asset outage:
   If the failing log indicates a 404 or Not Found on a release asset or `releases/latest` URL:
   * Classify as `upstream-asset`.
   * Rerun at most once.
   * Probe referenced provider repository using `gh api repos/<owner>/<repo>/releases/latest`.
   * Ensure label exists: `gh label create ci-infra -R devsy-org/devsy 2>/dev/null || true`.
   * Open or update a tracking issue on devsy with label `ci-infra`.
   * Record provider blockage for the daily digest.
   * Do not open issues in provider repositories.

2. Known transient infrastructure flake:
   Query persistent memory:
   Write to `xd://recall` with `{"query": "devsy ci failure fingerprint <fingerprint>"}`.
   If recognized as a known transient infrastructure flake, check `run_attempt`.
   If `run_attempt <= 2`, trigger a rerun:
   `gh run rerun <run_id> --failed`
   If `run_attempt > 2`, classify as persistent infrastructure failure with label `ci-infra`.

3. Deduplication and regression reopening:
   Search existing issues:
   `gh issue list -R devsy-org/devsy --state all --search "<fingerprint>" --json number,title,state`
   If an open issue matches, append run ID and head SHA:
   `gh issue comment <issue_number> -R devsy-org/devsy --body "### Recurrence Detected\nFailure recurred on main in run <run_id> at commit <head_sha>.\ndevsy-fingerprint: <fingerprint>"`
   Do not create duplicate issues.
   If a closed issue matches and the failure recurs on main, reopen the issue:
   `gh issue reopen <issue_number> -R devsy-org/devsy --comment "### Recurrence Detected\nFailure recurred on main in run <run_id> at commit <head_sha>.\ndevsy-fingerprint: <fingerprint>"`
   Only open a new issue when zero matches exist across open and closed states:
   Ensure labels exist:
   `gh label create ci -R devsy-org/devsy 2>/dev/null || true`
   `gh label create bug -R devsy-org/devsy 2>/dev/null || true`
   Create tracking issue:
   ```bash
   gh issue create -R devsy-org/devsy \
     --title "ci: <test_name> failure in <workflow_name>" \
     --label "ci,bug" \
     --body "$(cat <<'EOF'
   Summary: Automated triage detected CI failure on main in run <run_id> at commit <head_sha>.
   devsy-fingerprint: <fingerprint>

   Failure Signature:
   Workflow: <workflow_name>
   Job: <job_name>
   Step: <step_name>

   Log Excerpt:
   <bounded_log_extract>

   Next Actions:
   Triaged for root-cause investigation.
EOF
   )"
   ```

4. Memory hygiene:
   When an issue closes, retain status `resolved` with the fingerprint and closing run.
   Never retain issue numbers as permanent durable facts.
   Before acting on any recalled fingerprint, confirm it against live `gh issue list` results. A newer fact supersedes an older one.
