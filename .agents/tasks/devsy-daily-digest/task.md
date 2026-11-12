---
kind: task
id: devsy-daily-digest
name: devsy-daily-digest
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

# Devsy Daily Digest and Memory Consolidation

This task consolidates durable triage facts into persistent memory and outputs the structured daily markdown report summarizing garbage collection, pull request gates, main branch health, and active investigations.
Target project: devsy-org/devsy (Paseo project ID: prj_5e6fbfb713103941).

## Operational Boundaries

* Use memory only for durable knowledge: normalized failure fingerprints, flake classifications, and verified root causes.
* Never retain tracker identifiers or issue numbers as permanent durable facts.
* Do not perform loops or polling. Generate the report and finish.

## Execution Procedure

### 1. Memory Consolidation

Persist durable triage conclusions into persistent memory using the memory tool:
* New failure fingerprints: write to `xd://retain` with `{"facts": [{"fact": "devsy ci fingerprint <fingerprint>: <classification>", "scope": "project"}]}`.
* Verified root causes: write to `xd://retain` with `{"facts": [{"fact": "devsy root cause for <investigation_subject>: <summary>", "scope": "project"}]}`.
* Resolved issues: write to `xd://retain` with `{"facts": [{"fact": "devsy ci fingerprint <fingerprint>: resolved by commit <sha>", "scope": "project"}]}`.

### 2. Format and Output Daily Digest

Output the daily report adhering strictly to this markdown structure:

```markdown
# Devsy Daily Triage Digest: YYYY-MM-DD

## Workspaces Garbage Collected
* Total archived: <count>
* Details: <workspace_id> (<branch>)

## Active PRs and Gates
* PR <number> (<head_sha>): CI <status>, Greptile <score>, CodeRabbit <status>, Gate <state>

## CI Health on main
* Total failing runs triaged: <count>
* Reruns triggered: <count>
* Issues opened or updated: <list>

## Active Investigations and Fixes
* Issue <id>: <tier> (<model>) - <status>
```
