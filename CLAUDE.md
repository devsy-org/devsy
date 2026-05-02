# Devsy

Devsy is a client-only tool for creating reproducible developer environments based on devcontainer.json on any backend (local Docker, Kubernetes, remote machines, cloud VMs). It has a Go CLI and a desktop app (Tauri + React).

## Repository Structure

```
cmd/           CLI command implementations (cobra commands)
pkg/           Core packages and libraries
providers/     Provider implementations (docker, kubernetes, pro)
desktop/       Desktop application (Tauri + React + TypeScript)
e2e/           End-to-end tests (Ginkgo framework)
examples/      Example devcontainer configurations
hack/          Build and development scripts
docs/          Documentation website
```

## Tech Stack

- **CLI**: Go 1.25+, Cobra commands, gRPC tunnel
- **Desktop**: Tauri (Rust backend) + React + TypeScript + Yarn
- **Build**: [Task](https://taskfile.dev/) runner, GoReleaser
- **Test**: `go test` (unit), Ginkgo v2 (e2e)
- **Lint**: golangci-lint v2 with strict config (cyclop max-complexity: 8, funlen, revive arg-limit: 4, result-limit: 3)
- **Pre-commit**: trailing-whitespace, commitlint, shellcheck, shfmt, actionlint, biome, gitleaks, golangci-lint

## Build Commands

```bash
task cli:build:dev          # Build CLI for development
task cli:build              # Build CLI for production
task cli:build:dev:pro      # Build with Pro features
task cli:test               # Run unit tests
task cli:test:e2e           # Run all e2e tests
task cli:test:e2e:focus -- "pattern"  # Run focused e2e tests
task cli:lint               # Run linters
task cli:lint:fix           # Auto-fix lint issues
task cli:format             # Format Go code
task cli:tidy               # Tidy go.mod
task desktop:build          # Build desktop app
task desktop:check          # Lint + format + typecheck desktop
task desktop:tauri:dev      # Desktop dev mode
```

## Commit Convention

Conventional commits enforced by commitlint. Allowed types:

`build`, `ci`, `docs`, `feat`, `fix`, `perf`, `refactor`, `style`, `test`, `chore`, `revert`, `bump`, `fixup`

Format: `type(scope): description` — e.g. `feat(exec): add devcontainer spec compliance`

Body max line length is unlimited. Commits are validated by pre-commit hooks.

## Linting Rules

golangci-lint v2 is configured with strict settings in `.golangci.yaml`:

- **Cyclomatic complexity**: max 8 (`cyclop`)
- **Function arguments**: max 4 (`revive/argument-limit`)
- **Function return values**: max 3 (`revive/function-result-limit`)
- **Enabled linters**: cyclop, decorder, dupl, errcheck, fatcontext, forbidigo, funcorder, funlen, goconst, gocritic, godot, gosec, lll, misspell, modernize, nestif, revive, staticcheck, unparam, unused, whitespace
- **Formatters**: gci, gofumpt, goimports, golines

Always run `task cli:lint` before committing Go changes. Use `task cli:lint:new` to lint only changes since origin/main.

## Testing

### Unit Tests

```bash
task cli:test
```

Tests live alongside source files as `*_test.go`. Race detection and coverage are enabled by default.

### E2E Tests

E2E tests use [Ginkgo v2](https://onsi.github.io/ginkgo/) and live in `e2e/tests/`. Suites cover: context, exec, up, down, ssh, logs, provider, tunnel, IDE, machine, and more.

```bash
task cli:test:e2e                       # All e2e tests
task cli:test:e2e:suite -- "suite-name" # Specific suite
task cli:test:e2e:focus -- "pattern"    # Pattern-focused
```

E2E tests require a built binary (`e2e/bin/devsy-linux-amd64`), which `task cli:test:e2e:build` creates.

For Kubernetes e2e tests, set up a Kind cluster first: `task cli:test:e2e:kind:setup`

## Go Patterns

- Commands are in `cmd/` as individual files (one command per file)
- Reusable logic goes in `pkg/` packages
- Provider implementations go in `providers/`
- Keep functions under cyclomatic complexity 8 — split into helpers when needed
- Max 4 function arguments — use option structs for more
- Max 3 return values
- Run `task cli:format` to auto-format with gofumpt + goimports + golines

## Desktop App (Tauri)

The desktop app is in `desktop/` with:
- Frontend: React + TypeScript (`desktop/src/`)
- Backend: Rust + Tauri (`desktop/src-tauri/`)
- Type generation: `task desktop:tauri:generate-types` generates TS types from Rust bindings

```bash
cd desktop && yarn install --frozen-lockfile  # Install deps
task desktop:tauri:dev                        # Dev mode
task desktop:check                            # Full lint/format/typecheck
```

## CI/CD

GitHub Actions workflows in `.github/workflows/`:
- `pr-ci.yml` — main PR CI (lint, test, build, e2e)
- `lint.yml` — standalone lint workflow
- `release.yml` — release pipeline with GoReleaser
- `commit.yml` — commit validation
- `pre-commit.yml` — pre-commit hook validation

## Provider Architecture

Providers implement the driver interface in `pkg/driver/`. Current providers:
- `docker` — local Docker backend
- `kubernetes` — Kubernetes cluster backend
- `pro` — Devsy Pro managed backend

Custom providers: see `providers/providers.go` and [provider development docs](https://devsy.sh/docs/developing-providers/quickstart).
