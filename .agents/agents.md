# .agents

Platform-agnostic configuration for Devsy's autonomous agent workflows, following the
[AGENTS.md](https://agents.md/) and [.agents Protocol](https://dotagentsprotocol.com/#structure)
specifications.

## Layout

```
.agents/
├── agents.md          # this index + conventions
├── agents/<id>/       # sub-agent profiles (agent.md)
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
