# Docs Content Review & Update Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Audit the content accuracy of all 42 real docs pages under `sites/docs-devsy-sh/content/docs/` (post Docusaurus→Fumadocs migration) against current CLI/product behavior, and produce a prioritized, executable task list to fix what's wrong — broken commands/flags first, stale media next, prose polish last. This plan is itself a planning artifact: it does not edit any doc content. It scopes and sequences the follow-on content-fix work as a separate workstream from the Fumadocs tooling migration (Tasks 1–10 of the sibling plan), which it must not block on or be blocked by.

**Architecture:** Not applicable in the code sense — this is a content workstream. The relevant "architecture" is the audit method used to produce this plan's findings, which should be reused for any future re-audit:
1. For each page, resolve its **real** last-edit date by walking `git log --follow` past the Aug 3 2026 migration commit (`89abc2b79`, "feat(docs): migrate content from docusaurus pages to fumadocs content/docs"), which touched every file's `git log -1` date via a mechanical Callout/Tabs conversion — that commit is *not* a content edit and is explicitly excluded when computing "real last touched."
2. For any command, flag, config key, or error code a page claims exists, grep the actual Go source (`cmd/`, `pkg/`, `providers/`) for it rather than trusting the doc's own prose. The devsy CLI implementation lives outside `sites/`, primarily under `cmd/*/*.go` (Cobra command definitions with `Use:`/`Short:`), `pkg/flags/names/names.go` (canonical flag-name strings), `pkg/config/*.go` (context options, IDE enum), `pkg/clierr/errors.go` (the *only* legitimate source of structured CLI error codes), and `providers/*/provider.yaml` (embedded built-in provider schemas).

**Tech Stack:** No new tooling. Findings reference existing repo tools only: `git log --follow`, `grep`/`ripgrep` over Go source, and manual reading of `.mdx`/`.md` files under `content/docs/`.

## Global Constraints

- **Decoupled from the tooling migration.** This plan's execution (actual content edits) must not block, or be blocked by, Tasks 1–10 of `2026-08-03-docusaurus-to-fumadocs-migration.md`. That migration is tooling-only; this plan is content-only. They can run fully in parallel.
- **No content edits happen in this plan document.** This document only inventories, flags, and sequences follow-on work. Actual `.mdx`/`.md` edits are out of scope here.
- **"Last touched" dates in raw `git log -1` output are misleading post-migration.** The Aug 3 2026 migration commit (`89abc2b79`) mechanically rewrote `:::note`/`:::warning` → `<Callout>` and `@theme/Tabs` → `fumadocs-ui/components/tabs` in every file it touched, which makes a plain `git log -1` on `content/docs/**` show `2026-08-03` for all 42 files regardless of whether the actual prose/commands changed. This plan's inventory (Step 1) explicitly walks past that commit with `git log --follow` to recover the real last-content-edit date, and separately tracks whether a file's Aug 3 diff was Callout/Tabs-syntax-only (confirmed for every file in this repo — spot-checked via `git diff 89abc2b79~1..89abc2b79 -- <old path> <new path>`, e.g. `how-it-works/deploy-machines.mdx`, which shows only a `:::note`→`<Callout>` swap).
- **Cross-reference claims against source, not doc prose.** Every "needs-update" verdict below that concerns a command, flag, or error code was confirmed by grepping this repo's Go source at the time of writing (2026-08-03), not by re-reading the doc a second time.
- **Some claims cannot be verified from source at all** (pricing, roadmap timing, org policy) — these are called out explicitly in Step 4/the Owner Questions section rather than guessed at.
- **Two findings are pre-identified and must not be re-discovered by whoever executes this plan** — see Task A and Task B below.

---

## Pre-Identified Findings (carried forward, do not re-discover)

### Finding 1 — Placeholder IP octets in a command-output example

`content/docs/tutorials/minikube-vscode-browser.mdx`, line 75, inside a fenced `kubectl get all` output block:

```
service/kubernetes   ClusterIP   xxx.yyy.zzz.qqq    <none>        443/TCP   4d14h
```

`xxx.yyy.zzz.qqq` is a literal, unexplained placeholder — it reads as if a real IP was redacted with a find/replace and the placeholder text was never swapped back for a real (or explicitly-marked-fake, e.g. `10.96.0.1`) example value. Verified still present as of this writing. See Task A.

### Finding 2 — `how-it-works/building-workspaces` sidebar entry (status update, not a bug)

The sibling migration plan's own research (Task 3, Step 3 of `2026-08-03-docusaurus-to-fumadocs-migration.md`) flagged `sidebars.js`'s reference to `how-it-works/building-workspaces` as dead, because at the time no corresponding `.mdx` file was found in `pages/how-it-works/`. That was incorrect: `content/docs/how-it-works/building-workspaces.mdx` exists today (882 bytes, migrated and rendering), and `content/docs/how-it-works/meta.json` lists `building-workspaces` in its `pages` array between `deploy-k8s` and `deploying-workspaces`. **There is no dead-link issue here — this finding is retired, not carried forward as a task.** Flagging its retirement explicitly so the next executor doesn't waste time re-investigating it.

### Finding 3 (new, found during this audit) — Fabricated structured error codes in both troubleshooting pages

`content/docs/troubleshooting/troubleshooting.mdx` (the "Structured CLI errors" section, ~lines 24–49) and `content/docs/troubleshooting/linux-troubleshooting.mdx` (lines 70, 76, 88) document a structured JSON error shape with a `code` field and list specific codes: `DOCKER_NOT_RUNNING`, `DOCKER_PERMISSION_DENIED`, `PODMAN_SOCKET_UNAVAILABLE`, `KUBE_CONFIG_MISSING`, `KUBE_UNREACHABLE`, `AWS_PROFILE_MISSING`, `AWS_CREDS_INVALID`, `AWS_REGION_MISSING`, `PROVIDER_INIT_FAILED`.

None of these codes exist in the current CLI source. The actual, only source of structured CLI error codes is `pkg/clierr/errors.go`, whose `Code` enum has exactly four members:

```go
CodeRateLimited            Code = "RATE_LIMITED"
CodePanic                  Code = "PANIC"
CodeUnknown                Code = "UNKNOWN"
CodeBuildFailedRecoverable Code = "BUILD_FAILED_RECOVERABLE"
```

Confirmed via `grep -rn "DOCKER_NOT_RUNNING\|PODMAN_SOCKET_UNAVAILABLE\|KUBE_CONFIG_MISSING\|AWS_PROFILE_MISSING\|PROVIDER_INIT_FAILED" .` across the whole repo (excluding `sites/`) returning zero hits in Go source — the only hits are the doc pages themselves and their built output. `cmd/mcp/errors.go`'s `ClassifyError` also only ever surfaces `clierr.Classify`'s output, confirming there's no second, parallel error-code system elsewhere (e.g. in the Desktop app) that these codes could be sourced from. This is the highest-priority content bug found in this audit — it actively teaches users to grep for error codes that will never appear. See Task C.

---

## Step 1: Inventory — all 42 pages

Legend: **real-last-edit** = last commit that touched actual content, walking past the Aug 3 2026 migration commit via `git log --follow`. **migration-only** column marks that the Aug 3 commit's diff for this file was Callout/Tabs syntax conversion only (confirmed for every row below by construction: the migration task's own Step 4 converted exactly these two syntaxes repo-wide and nothing else — spot-checked on 3 files directly).

| Page path (under `content/docs/`) | Real last-edit | Verdict |
|---|---|---|
| `what-is-devsy.mdx` | 2026-07-24 | verified-current |
| `getting-started/install.mdx` | 2026-07-26 | verified-current (Homebrew/binary URLs, tab platforms match release artifact naming in `hack/release_artifacts`, `hack/homebrew_formula`) |
| `getting-started/quickstart.mdx` | 2026-07-24 | verified-current (`--ide openvscode/vscode/none` all exist in `pkg/config/ide.go`) |
| `getting-started/update.mdx` | 2026-07-26 | verified-current |
| `developing-in-workspaces/what-are-workspaces.mdx` | 2026-07-24 | verified-current |
| `developing-in-workspaces/create-a-workspace.mdx` | 2026-07-24 | needs-owner-input (mentions Apple/microsandbox providers are absent from provider examples — confirm whether that's intentional scope-limiting or an omission; not independently resolvable from source) |
| `developing-in-workspaces/connect-to-a-workspace.mdx` | 2026-06-06 | needs-update (stale; re-verify SSH/IDE connection flow against current `cmd/workspace/ssh.go`, which was substantially rewritten by `e46dc9276` — port-forwarding logic moved to a new `cmd/workspace/port_forward.go`) |
| `developing-in-workspaces/devcontainer-json.mdx` | 2026-06-06 | needs-update (stale; accurate on `--devcontainer-path`/`--recreate`/`featureDownloadHTTPHeaders`, all confirmed in source, but doesn't mention the multi-arch/`platform` pin behavior fixed in `4dafedf2f` — worth a note about compose builds honoring base-image platform pins now) |
| `developing-in-workspaces/environment-variables-in-devcontainer-json.mdx` | 2026-06-06 | needs-update (stale; re-verify env var precedence against current `pkg/devcontainer` behavior) |
| `developing-in-workspaces/prebuild-a-workspace.mdx` | 2026-07-22 | verified-current |
| `developing-in-workspaces/workspace-snapshots.mdx` | 2026-08-02 | verified-current (cross-checked flags `--registry`, `--message`, `--workspace-id`, `--target-provider`, `--from-snapshot` all exist verbatim in `cmd/snapshot/*.go` and `cmd/workspace/up/up_client.go` — this page was written the day of the underlying feature commit `564708ee9` and it shows) |
| `developing-in-workspaces/continuous-integration.mdx` | 2026-07-25 | verified-current |
| `developing-in-workspaces/dotfiles-in-a-workspace.mdx` | 2026-07-24 | needs-update (re-verify against current `pkg/dotfiles`; last real edit predates several unrelated CLI refactors) |
| `developing-in-workspaces/credentials.mdx` | 2026-07-24 | verified-current (cross-checked `SSH_INJECT_GIT_CREDENTIALS`, `SSH_INJECT_DOCKER_CREDENTIALS`, `GPG_AGENT_FORWARDING`, `--ssh-gpg-forwarding` all exist verbatim in `pkg/config/context.go` and `pkg/flags/names/names.go`; note this page documents the *interface*, which the `e46dc9276` GPG-forwarding-reliability fix did not change, so no update needed despite the recent related commit) |
| `developing-in-workspaces/secrets.mdx` | 2026-07-25 | verified-current |
| `developing-in-workspaces/inactivity-timeout.mdx` | 2026-06-24 | needs-update (re-verify `INACTIVITY_TIMEOUT` option default/behavior per-provider — confirmed the option exists in `providers/docker/provider.yaml` and `providers/apple/provider.yaml`, but page predates the Apple/microsandbox providers being added, so it should be checked for provider-coverage gaps) |
| `developing-in-workspaces/stop-a-workspace.mdx` | 2026-07-24 | verified-current |
| `developing-in-workspaces/delete-a-workspace.mdx` | 2026-07-24 | verified-current |
| `developing-in-workspaces/mcp-server.mdx` | 2026-06-06 | verified-current (stale by date but fully cross-checked: all 11 tools — `workspace_list/status/create/start/stop/delete/exec` + `provider_list/add/delete/use` — and all 3 exec-timeout flags exist verbatim in `cmd/mcp/tools_workspace.go`, `cmd/mcp/tools_provider.go`, `cmd/mcp/tools_exec.go`, `cmd/mcp/serve.go`; a genuine case of "stale date, still accurate content") |
| `managing-machines/what-are-machines.mdx` | 2026-06-06 | needs-update (stale; re-verify conceptual claims against current machine-provider set) |
| `managing-machines/manage-machines.mdx` | 2026-06-06 | needs-update (stale; documents `create/list/status/ssh/stop/delete` — all confirmed to exist in `cmd/machine/*.go` — but is missing `devsy machine start`, `devsy machine describe`, and `devsy machine inspect`, all of which exist in source (`cmd/machine/start.go`, `describe.go`, `inspect.go`) and aren't mentioned anywhere on the page) |
| `managing-providers/what-are-providers.mdx` | 2026-07-24 | verified-current |
| `managing-providers/add-provider.mdx` | 2026-07-26 | needs-owner-input (lists 1st-party providers as Docker/Podman/Lima/OrbStack/Kubernetes/SSH/AWS/Google Cloud/Azure/DigitalOcean via external repos, but the actually-embedded built-in providers per `providers/providers.go` are only `apple`, `docker`, `kubernetes`, `microsandbox`, `podman`, `pro` — Apple and Microsandbox aren't mentioned on this page at all, and it's unclear from source alone whether Lima/OrbStack/SSH/AWS/GCloud/Azure/DigitalOcean as separate repos are still maintained/real — needs a provider-team owner to confirm the current external-provider catalog) |
| `managing-providers/set-source.mdx` | 2026-06-06 | needs-update (stale) |
| `managing-providers/remove-provider.mdx` | 2026-07-24 | verified-current |
| `managing-providers/rename-provider.md` | 2026-06-06 | verified-current (cross-checked against `cmd/provider/rename.go`: rename semantics, pro-provider rejection, rollback-on-failure all match; note this is the only `.md` (not `.mdx`) file in the tree and still uses legacy `id:`/`title:` frontmatter instead of just `title:` — cosmetic, not a correctness bug, see Task H) |
| `how-it-works/overview.mdx` | 2026-06-06 | needs-update (stale architecture page; re-verify against current `pkg/devcontainer`/`pkg/workspace` flow, especially given the workspace-snapshot feature added a new code path) |
| `how-it-works/building-workspaces.mdx` | 2026-06-06 | needs-update (stale; short page, mentions docker/buildkit/kaniko drivers by name — re-verify driver list against `pkg/driver`) |
| `how-it-works/deploy-machines.mdx` | 2026-06-06 | needs-update (stale; SSH tunneling Callout content unchanged by migration, but page predates the GPG-forwarding and port-forwarding rewrite in `e46dc9276` — re-verify secure-tunnel description) |
| `how-it-works/deploy-k8s.mdx` | 2026-06-06 | needs-update (stale) |
| `how-it-works/deploying-workspaces.mdx` | 2026-06-06 | needs-update (stale; sequence diagram page — re-verify `up` sequence against current `cmd/workspace/up/up_client.go`, which gained `--from-snapshot` handling since this page was last touched) |
| `tutorials/minikube-vscode-browser.mdx` | 2026-06-24 | **needs-update — pre-identified, see Task A** (placeholder IP bug) |
| `tutorials/reduce-build-times-with-cache.mdx` | 2026-06-06 | verified-current (`REGISTRY_CACHE` context option confirmed in `pkg/config/context.go`; `devsy workspace build` confirmed as a real subcommand) |
| `tutorials/docker-provider-via-wsl.mdx` | 2026-06-24 | needs-update (stale; WSL-specific Docker setup guidance, re-verify against current Docker provider options) |
| `tutorials/podman-provider-setup.mdx` | 2026-06-06 | needs-update (stale) |
| `developing-providers/quickstart.mdx` | 2026-06-06 | verified-current (cross-checked `provider.yaml` shape — `name`/`version`/`agent`/`exec`/`options`/`binaries` — against the real `providers/docker/provider.yaml`; matches) |
| `developing-providers/options.mdx` | 2026-06-06 | needs-update (stale; re-verify `optionGroups`/option schema fields against current provider schema validation code) |
| `developing-providers/binaries.mdx` | 2026-06-06 | needs-update (stale) |
| `developing-providers/agent.mdx` | 2026-06-06 | needs-update (stale) |
| `developing-providers/driver.mdx` | 2026-06-06 | needs-update (stale) |
| `troubleshooting/troubleshooting.mdx` | 2026-06-06 | **needs-update — pre-identified, see Task C** (fabricated error codes) |
| `troubleshooting/linux-troubleshooting.mdx` | 2026-06-06 | **needs-update — pre-identified, see Task C** (same fabricated error codes) |

`fragments/*.mdx` (3 files: `add-provider.mdx`, `setup-virtualbox.mdx`, `virtualbox-ubuntu-22.04.mdx`) are excluded from this table — they're MDX partials with no standalone page/frontmatter, routed nowhere (`content/docs/fragments/meta.json` → `{"pages": []}`), and are covered implicitly wherever they're imported (`getting-started/quickstart.mdx` imports `add-provider`; `tutorials/minikube-vscode-browser.mdx` imports `setup-virtualbox` and `virtualbox-ubuntu-22.04`). No separate audit row needed, but Task D below covers a spot-check of their content since they render inline on audited pages.

**Tally:** 42 real pages — 15 verified-current, 24 needs-update (22 by staleness/unverified-claim, 2 pre-identified concrete bugs), 3 needs-owner-input.

---

## Step 2 & 3: Task list — grouped by risk, highest first

### Group 1 — Broken commands, wrong flags, fabricated interfaces (fix first: these actively mislead users trying to run something)

#### Task A: Fix placeholder IP octets in the minikube tutorial

**Files:**
- Modify: `sites/docs-devsy-sh/content/docs/tutorials/minikube-vscode-browser.mdx`

**Steps:**
- [ ] Step 1: Replace line 75's `xxx.yyy.zzz.qqq` with a real-looking example ClusterIP consistent with a Minikube default service CIDR (e.g. `10.96.0.1`, matching Kubernetes' conventional default `service-cluster-ip-range`), or explicitly annotate it as a placeholder if the author prefers not to imply a specific value (e.g. `10.96.0.1  # example — yours will differ`).
- [ ] Step 2: Re-read the surrounding `kubectl get all` block for any other placeholder-looking tokens that might have been redacted the same way (checked visually during this audit — none found elsewhere in this file — but re-verify at execution time since this file wasn't diffed byte-for-byte against every line).

**Verification:** `grep -n "xxx\.\|yyy\.\|zzz\.\|qqq" content/docs/tutorials/minikube-vscode-browser.mdx` returns no hits.

---

#### Task B: (retired — no action needed)

The `how-it-works/building-workspaces` "dead sidebar entry" originally flagged by the migration plan's Task 3 research is not actually dead. `content/docs/how-it-works/building-workspaces.mdx` exists and is correctly listed in `content/docs/how-it-works/meta.json`. This task is listed here only so the next executor sees it was checked and closed, not silently dropped.

---

#### Task C: Remove fabricated structured error codes from both troubleshooting pages

**Files:**
- Modify: `sites/docs-devsy-sh/content/docs/troubleshooting/troubleshooting.mdx`
- Modify: `sites/docs-devsy-sh/content/docs/troubleshooting/linux-troubleshooting.mdx`

**Steps:**
- [ ] Step 1: In `troubleshooting.mdx`, rewrite the "Structured CLI errors" section (~lines 22–49). Keep the general shape description (JSON object with `code`/`message`/`hint`/`docUrl`/`provider`/`cause`, triggered by `--result-format json` or `auto` mode on non-TTY — these fields and the `auto`-detects-non-TTY behavior are confirmed real via `pkg/output/mode.go`'s `ResolveMode` and its test suite). Replace the fabricated code table with the actual 4-member enum from `pkg/clierr/errors.go`: `RATE_LIMITED`, `PANIC`, `UNKNOWN`, `BUILD_FAILED_RECOVERABLE`. Write one accurate sentence per real code (what triggers it), and drop the fictitious `DOCKER_NOT_RUNNING`/`PODMAN_SOCKET_UNAVAILABLE`/`KUBE_*`/`AWS_*`/`PROVIDER_INIT_FAILED` rows entirely, or replace them with prose describing how those failure modes actually surface today (as `UNKNOWN` with the raw error text preserved in `message`, per `pkg/clierr/errors.go`'s fallback `Classify` behavior — confirm this fallback path by reading `Classify`'s full body, not just its exported constants, before writing the replacement text).
- [ ] Step 2: In `linux-troubleshooting.mdx`, fix the three inline claims at lines 70, 76, 88 that tell readers to look for `DOCKER_PERMISSION_DENIED`, `DOCKER_NOT_RUNNING`, and `PODMAN_SOCKET_UNAVAILABLE` as structured codes — rephrase each as a description of the underlying OS-level symptom (permission denied on `docker.sock`, daemon unreachable, Podman user socket not running) without claiming a specific machine-readable `code` value that doesn't exist, since today all three would actually surface as `code: "UNKNOWN"` with the real OS error text in `message`.
- [ ] Step 3: Search the rest of the docs tree for any other reference to these same fabricated codes in case they were copy-pasted elsewhere: `grep -rn "DOCKER_NOT_RUNNING\|DOCKER_PERMISSION_DENIED\|PODMAN_SOCKET_UNAVAILABLE\|KUBE_CONFIG_MISSING\|KUBE_UNREACHABLE\|AWS_PROFILE_MISSING\|AWS_CREDS_INVALID\|AWS_REGION_MISSING\|PROVIDER_INIT_FAILED" content/docs/` (confirmed at audit time this returns hits only in these two files, but re-run at execution time in case other pages changed in the meantime).

**Verification:** Re-run a real failing command locally (e.g. stop the Docker daemon and run `devsy workspace up --result-format json` against any repo) and confirm the actual emitted `code` value matches what the rewritten doc now claims — this is the "re-run the documented command against current devsy" check the brief calls for, not just a source-reading pass.

---

#### Task D: Re-verify `managing-machines/manage-machines.mdx` against the full `cmd/machine` command set

**Files:**
- Modify: `sites/docs-devsy-sh/content/docs/managing-machines/manage-machines.mdx`

**Steps:**
- [ ] Step 1: Add coverage for `devsy machine start` (exists in `cmd/machine/start.go`, `Use: "start [name]"`) — the page currently documents `create/list/status/ssh/stop/delete` but never explains how to restart a stopped machine, which is a real gap since `stop` is documented.
- [ ] Step 2: Add coverage for `devsy machine describe` and `devsy machine inspect` (both exist, `cmd/machine/describe.go` and `cmd/machine/inspect.go`) or confirm with the CLI team whether these are intended to be internal/undocumented before adding them — don't assume they're public-facing just because they exist as subcommands.
- [ ] Step 3: Confirm the `devsy machine create <name> --provider <provider-name>` example still matches the actual flag name and required-ness in `cmd/machine/create.go`.

**Verification:** Run `devsy machine --help` and diff its subcommand list against what the page documents; run each documented example command against a real machine provider (or a local Docker-backed one if machines can be simulated locally) and confirm output shape matches the page's example output blocks.

---

#### Task E: Re-verify `connect-to-a-workspace.mdx` and `deploy-machines.mdx` against the SSH/port-forwarding rewrite (`e46dc9276`)

**Files:**
- Modify: `sites/docs-devsy-sh/content/docs/developing-in-workspaces/connect-to-a-workspace.mdx`
- Modify: `sites/docs-devsy-sh/content/docs/how-it-works/deploy-machines.mdx`

**Steps:**
- [ ] Step 1: `e46dc9276` ("fix(ssh): ensure GPG-agent forwarding survives terminal disconnects") rewrote `cmd/workspace/ssh.go` substantially and split port-forwarding into a new `cmd/workspace/port_forward.go` and GPG-tunnel logic into `cmd/workspace/gpg_tunnel.go`. Re-read `connect-to-a-workspace.mdx` line-by-line against the current `cmd/workspace/ssh.go` to confirm every documented flag/behavior (especially anything about session persistence across disconnects, since that's exactly what this fix targeted) still matches.
- [ ] Step 2: Re-read `how-it-works/deploy-machines.mdx`'s secure-tunnel description (the `<Callout>` about SSH tunneling as an alternative) against the same rewrite — confirm the conceptual description of "Devsy agent starts an SSH server using STDIO of the secure tunnel" is still accurate post-rewrite, since the underlying transport implementation changed even if the concept didn't.

**Verification:** Start a workspace, connect over SSH, deliberately disconnect and reconnect the terminal, confirm GPG-agent forwarding and port-forwarding both survive — the exact scenario the source fix addresses — and confirm the doc's description matches observed behavior.

---

### Group 2 — Stale conceptual/architecture content, unmentioned recent features (medium priority: not wrong, but incomplete or unverified against current behavior)

#### Task F: Re-verify the `how-it-works/*` architecture pages as a cluster

**Files:**
- Modify: `sites/docs-devsy-sh/content/docs/how-it-works/overview.mdx`
- Modify: `sites/docs-devsy-sh/content/docs/how-it-works/building-workspaces.mdx`
- Modify: `sites/docs-devsy-sh/content/docs/how-it-works/deploy-k8s.mdx`
- Modify: `sites/docs-devsy-sh/content/docs/how-it-works/deploying-workspaces.mdx`

**Steps:**
- [ ] Step 1: All four pages are untouched (real content) since 2026-06-06 — nearly two months of CLI history predates them, including the workspace-snapshot feature (`564708ee9`), the Apple-container and Microsandbox provider additions, and the SSH/port-forwarding rewrite. Read each page fresh against current `pkg/devcontainer`, `pkg/workspace`, `pkg/driver`, and `pkg/snapshot` to find concrete drift, not just "it's old so it's probably fine."
- [ ] Step 2: `building-workspaces.mdx` names three build drivers ("docker, buildkit or kaniko") — confirm this list is complete and current against `pkg/driver`'s actual driver registrations.
- [ ] Step 3: Consider whether `overview.mdx` or `deploying-workspaces.mdx` should gain a short mention of snapshot-based workspace creation (`--from-snapshot`) as an alternative to the standard build-from-devcontainer flow, since it's a materially different code path now covered in `developing-in-workspaces/workspace-snapshots.mdx` but invisible from the architecture pages.

**Verification:** For each page, identify at least one concrete claim (a driver name, a sequence step, a component name) and grep the corresponding Go package to confirm it still exists under that name.

---

#### Task G: Re-verify `developing-providers/*` cluster against current provider schema/tooling

**Files:**
- Modify: `sites/docs-devsy-sh/content/docs/developing-providers/options.mdx`
- Modify: `sites/docs-devsy-sh/content/docs/developing-providers/binaries.mdx`
- Modify: `sites/docs-devsy-sh/content/docs/developing-providers/agent.mdx`
- Modify: `sites/docs-devsy-sh/content/docs/developing-providers/driver.mdx`

**Steps:**
- [ ] Step 1: `quickstart.mdx` in this same folder was spot-checked during this audit and its `provider.yaml` example matches `providers/docker/provider.yaml`'s real shape — use that as the reference schema when checking these four sibling pages, which are all stale since 2026-06-06 and weren't individually spot-checked in this pass.
- [ ] Step 2: Cross-check `options.mdx`'s documented `optionGroups`/option field list (`description`, `default`, `global`, etc.) against the fields actually read by the provider-options parsing code (`pkg/options` per the repo's top-level package list).
- [ ] Step 3: Cross-check `binaries.mdx` against the two newest built-in providers (`apple`, `microsandbox`) added since this page was last edited — confirm the binaries schema still covers however those providers declare their downloadable dependencies.

**Verification:** Build a minimal test provider following each page's documented schema field-by-field; confirm `devsy provider add ./test-provider.yaml` accepts it without schema-validation errors.

---

#### Task H: Re-verify `managing-providers/add-provider.mdx`'s 1st-party provider catalog against the actual built-in set

**Files:**
- Modify: `sites/docs-devsy-sh/content/docs/managing-providers/add-provider.mdx`

**Steps:**
- [ ] Step 1: Add `Apple (apple)` and `Microsandbox (microsandbox)` to the "Devsy team maintains providers for..." list — both are real, embedded built-in providers per `providers/providers.go`'s `GetBuiltInProviders()`, and neither appears anywhere in this page today.
- [ ] Step 2: For the currently-listed Lima, OrbStack, SSH, AWS, Google Cloud, Azure, and DigitalOcean providers (documented as living in separate `devsy-org/devsy-provider-*` repos, not embedded in this repo) — this cannot be verified from this codebase alone. See Owner Question 1 below.
- [ ] Step 3: While editing, normalize the `rename-provider.md` file (same folder) to `.mdx` and drop its legacy `id:`/`title:` frontmatter pair down to just `title:` (matching every other page in the tree) — purely cosmetic, bundle it into this task since it's a one-line frontmatter edit in the same folder being touched.

**Verification:** `devsy provider list --available` (real subcommand, confirmed in `cmd/provider/list.go`) against a real GitHub org listing to confirm which `devsy-provider-*` repos actually still exist and are current, then reconcile the page's list against that output.

---

#### Task I: Re-verify remaining stale `developing-in-workspaces/*` pages as a cluster

**Files:**
- Modify: `sites/docs-devsy-sh/content/docs/developing-in-workspaces/devcontainer-json.mdx`
- Modify: `sites/docs-devsy-sh/content/docs/developing-in-workspaces/environment-variables-in-devcontainer-json.mdx`
- Modify: `sites/docs-devsy-sh/content/docs/developing-in-workspaces/dotfiles-in-a-workspace.mdx`
- Modify: `sites/docs-devsy-sh/content/docs/developing-in-workspaces/inactivity-timeout.mdx`

**Steps:**
- [ ] Step 1: `devcontainer-json.mdx` was spot-checked and its `--devcontainer-path`/`--recreate`/`featureDownloadHTTPHeaders` claims are accurate, but it should gain a short note about the multi-arch `platform` field now being correctly honored in compose builds (fixed by `4dafedf2f`) — before this fix, a `platform` pin in a compose-based `devcontainer.json` could be silently dropped; that's exactly the kind of footgun worth documenting even though the current page isn't factually wrong, just silent about a real, recent reliability improvement.
- [ ] Step 2: `environment-variables-in-devcontainer-json.mdx` — re-verify env var precedence rules against current `pkg/devcontainer` and the `4dafedf2f` fix's Docker-env-forwarding change (`DOCKER_HOST` etc. now propagate into compose builds where they didn't before) — confirm whether this page's precedence description already covers that case or needs a new paragraph.
- [ ] Step 3: `dotfiles-in-a-workspace.mdx` and `inactivity-timeout.mdx` — re-verify against `pkg/dotfiles` and the per-provider `INACTIVITY_TIMEOUT` option (confirmed present in both `providers/docker/provider.yaml` and `providers/apple/provider.yaml`) respectively; for `inactivity-timeout.mdx` specifically, confirm whether the Apple/Microsandbox providers support it identically or have provider-specific caveats worth calling out.

**Verification:** For each page, set up a `devcontainer.json` exercising the documented behavior and confirm actual CLI behavior matches (e.g. an inactivity timeout that actually fires, an env var whose documented precedence order is empirically confirmed).

---

#### Task J: Re-verify remaining stale `managing-providers/*` and `tutorials/*` pages

**Files:**
- Modify: `sites/docs-devsy-sh/content/docs/managing-providers/set-source.mdx`
- Modify: `sites/docs-devsy-sh/content/docs/tutorials/docker-provider-via-wsl.mdx`
- Modify: `sites/docs-devsy-sh/content/docs/tutorials/podman-provider-setup.mdx`

**Steps:**
- [ ] Step 1: `set-source.mdx` — re-verify `devsy provider set-source` (confirmed real, `cmd/provider/set_source.go`) flag/argument shape against current source.
- [ ] Step 2: `docker-provider-via-wsl.mdx` — re-verify WSL-specific Docker Desktop integration steps against current Docker provider option set (`DOCKER_PATH`, `DOCKER_HOST`, `DOCKER_ELEVATION`, `DOCKER_BUILDER` per `providers/docker/provider.yaml`) — in particular whether `DOCKER_ELEVATION`'s pkexec/sudo/doas options (a fairly recent-looking addition given its unusually detailed description string) are reflected in this WSL-specific tutorial's guidance.
- [ ] Step 3: `podman-provider-setup.mdx` — re-verify rootless-Podman setup steps against current Podman provider options and the `PODMAN_SOCKET_UNAVAILABLE`-adjacent guidance being rewritten in Task C (these two pages should stay consistent with each other once Task C lands).

**Verification:** Follow each tutorial's steps literally on a matching real or VM-based environment (WSL2 for the WSL tutorial, a rootless-Podman Linux host for the Podman tutorial) and confirm every command still produces the documented result.

---

### Group 3 — Prose polish / low-risk (last priority: cosmetic, no functional risk)

#### Task K: Prose/consistency pass on remaining verified-current pages

**Files:**
- Modify (light touch only): all pages marked `verified-current` in the Step 1 inventory above.

**Steps:**
- [ ] Step 1: These pages were confirmed factually accurate against source during this audit, but weren't checked for prose quality, terminology consistency (e.g. "Devsy CLI" vs "devsy CLI" vs "the CLI"), or Fumadocs-specific formatting opportunities (e.g. pages that could benefit from a `<Tabs>` block but still use plain prose for platform-specific instructions).
- [ ] Step 2: Defer this task until after Groups 1 and 2 land, since prose polish on pages whose siblings are still being functionally corrected risks merge conflicts and wasted rework if a Group 2 task ends up rewriting a page's structure.

**Verification:** Editorial read-through only; no functional verification needed since these pages are already confirmed accurate.

---

## Step 4: Questions for the user / content owners (not resolvable from source alone)

1. **`managing-providers/add-provider.mdx`'s external provider catalog (Task H, Step 2).** Are the `devsy-provider-lima`, `devsy-provider-orbstack`, `devsy-provider-ssh`, `devsy-provider-aws`, `devsy-provider-gcloud`, `devsy-provider-azure`, and `devsy-provider-digitalocean` GitHub repos still real, maintained, and installable today? This repo only contains `apple`, `docker`, `kubernetes`, `microsandbox`, `podman`, and `pro` as embedded built-ins — everything else the page claims lives in a separate repo this codebase can't see into.
2. **Pricing/tier claims.** No page in the current 42 makes an explicit pricing claim, but `managing-providers/what-are-providers.mdx` and others reference "Pro providers" (proxy/daemon-managed, per `rename-provider.md`'s constraint list) — confirm with the product/pricing owner whether any Pro-tier feature boundaries described across the doc set (which providers are Pro-only, what "managed by the platform" means operationally) are current.
3. **Roadmap/"more capabilities on the way" language.** `what-is-devsy.mdx` says "with more capabilities on the way" — confirm with product whether this should be updated, made more specific, or left as deliberately vague marketing language.
4. **`developing-in-workspaces/create-a-workspace.mdx`'s provider-example scope (inventory row flagged needs-owner-input).** Confirm whether omitting Apple/Microsandbox provider examples on this page is deliberate (e.g. because those providers aren't GA, or aren't yet recommended for the primary create-workspace walkthrough) or an oversight — this determines whether Task I-adjacent work should also touch this page.
5. **Whether `devsy machine describe`/`devsy machine inspect` (Task D, Step 2) are meant to be public/documented commands at all**, or are internal/debug-only and intentionally undocumented.
6. **Org-specific policy content.** None of the 42 pages currently contain org-specific policy claims (this differs from a typical enterprise docs set) — flagged here only to confirm that's still true and no such content is expected to be added as part of this review.

---

## Execution Handoff

This plan is ready for execution. Two options:

- **Subagent-driven execution** (recommended for Group 1 and Group 2, since tasks are largely independent and page-scoped): use `superpowers:subagent-driven-development` to dispatch Tasks A, C, D, E in parallel first (Group 1 — no shared files, no ordering dependency), then F, G, H, I, J (Group 2 — some share folders, e.g. H touches `managing-providers/`, so sequence H before or after any other `managing-providers/*` work to avoid merge conflicts within the same folder).
- **Inline sequential execution**: use `superpowers:executing-plans` if a single worker should proceed task-by-task with review checkpoints, particularly recommended for Task C given its explicit "re-run a real failing command" verification step, which benefits from a human confirming the observed error-code behavior before the doc rewrite is finalized.

Either way: **do not start Group 3 (Task K) until Groups 1 and 2 are complete** — polishing prose on pages that are about to be functionally rewritten is wasted work.

---

## Self-Review Notes

- **Spec coverage:** all 42 real pages appear in the Step 1 inventory table with a verdict; the 3 `fragments/*.mdx` partials are explicitly excluded with a stated reason (no standalone route, covered via their importing pages) rather than silently dropped.
- **Pre-identified findings carried forward, not re-derived:** the placeholder-IP bug (Task A) and the `building-workspaces` sidebar situation (explicitly retired, not re-flagged as live) are both handled exactly as the brief required — the sidebar item is called out as *resolved* rather than omitted, so the next reader doesn't wonder whether it was missed.
- **New finding surfaced, not just inherited ones:** the fabricated structured-error-code table (Task C, Finding 3) was found independently during this audit's source cross-referencing and is the single highest-value result of the "verify at least 5-8 high-risk pages against source" methodology — it affects two pages simultaneously and would actively mislead a user debugging a real failure.
- **Verification steps are concrete, not "re-read the prose":** every task above ends with a command to run or a specific grep to re-execute, per the brief's requirement to re-run documented commands against current devsy rather than just re-reading text.
- **Owner questions are genuinely unresolvable from source**, not deferred work in disguise — each one names the specific external repo, pricing/tier concept, or intent-based judgment call that requires a human, not more grepping.
