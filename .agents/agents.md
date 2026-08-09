# .agents

Platform-agnostic configuration for Devsy's autonomous agent workflows, following the
[AGENTS.md](https://agents.md/) and [.agents Protocol](https://dotagentsprotocol.com/#structure)
specifications.

## Layout

```
.agents/
├── agents.md          # this index + conventions
├── agents/<id>/       # sub-agent profiles (agent.md)
└── tasks/<id>/        # scheduled repeat tasks (task.md)
```

## Agents

| Agent | Scope | Packages / area |
|-------|-------|-----------------|
| `ci-optimizer` | `.github/workflows` + Action logs | CI |
| `cmd-reviewer` | `cmd/` | CLI commands |
| `pkg-ssh-git` | SSH, git, credentials, shells | `ssh`, `gitsshsigning`, `gitcredentials`, `credentials`, `pty`, `shell`, `dotfiles`, `gpg` |
| `pkg-container` | Docker / OCI / images | `docker`, `dockerinstall`, `dockercredentials`, `dockerfile`, `compose`, `image`, `extract`, `flatpak`, `driver` |
| `pkg-tunnel-network` | Tunneling + networking | `tunnel`, `http`, `netstat`, `port`, `inject` |
| `pkg-core-agent` | Agent runtime core | `agent`, `client`, `command`, `options`, `config`, `devsyconfig`, `task`, `workspace`, `template`, `provider` |
| `pkg-system-platform` | OS / platform / utilities | `platform`, `machineid`, `apple`, `selfupdate`, `version`, `status`, `snapshot`, `daemon`, `sharedfile`, `open`, `scanner`, `language`, `log`, `output`, `stdio`, `terminal`, `theme`, `table`, `survey`, `secrets`, `token`, `telemetry`, `exitcode`, `flags`, `hash`, `id`, `random`, `util`, `types`, `ts`, `envfile`, `encoding`, `compress`, `copy`, `file`, `download`, `clierr`, `clihelp`, `git`, `ide`, `devcontainer` |
| `integration-test` | `e2e/` | Ginkgo E2E + framework |
| `lint-fixer` | repo-wide lint | `golangci-lint`, `biome`, `prek` |
| `docs-keeper` | docs accuracy | `AGENTS.md`, `README.md`, `cmd/` help, `sites/` |
| `devcontainer-spec` | `.devcontainer/` | devcontainer spec compliance |
| `codefactor` | Codefactor issues | Go source flagged by Codefactor |
| `ui-polish` | desktop UI | `desktop/src/renderer/src/` (Svelte/TS) |
| `agent-analytics` | agent-fleet analytics | `hack/analytics/`, `.agents/`, `pkg/`/`cmd/` (data-driven only), OpenHands Cloud API (read-only) |

The `pkg/*` agents split `pkg/` into logical categories so multiple agents review in
parallel without overlapping scope.

## Tasks

Each `tasks/<id>/task.md` declares the task scope, not its schedule. The hosting platform
configures when and how the task runs.

## Conventions

### Frontmatter (validated by `agents_test.go`)

**`agents/<id>/agent.md`** — required: `id`, `name`, `description`, `enabled`.
`id` and `name` must equal the directory name (kebab-case).

**`tasks/<id>/task.md`** — required: `kind: task`, `id`.
`id` must equal the directory name. Optional `agent` links the task to an agent id.
Schedule fields are omitted; the hosting platform sets the schedule.

### Self-improvement loop

Every agent prompt ends with an instruction to, upon task completion:

1. Persist key findings / regressions / config gotchas for the next run by updating the
   running automation through the automation service (a configuration change, not a git
   commit) — e.g. `PATCH /api/automation/v1/{id}` — or via the service's agentic memory
   system when one is available. Run findings and memory are never committed to this repo.
2. If a finding changes how the task should be run, propose a `description` amendment to
   its own `agent.md` (a committed prompt change is acceptable) and surface it for human
   review.

This keeps the prompts from going stale while keeping transient run learnings with the
automation that produced them, out of git history.

### Platform-agnostic execution

Tasks declare scope, not schedules. A platform that reads the `.agents/` convention
configures and runs them. No platform-specific runtime lives here.
