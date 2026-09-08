# AGENTS.md

---

## Environment Setup

Devsy is a monorepo with a Go-based CLI and an Electron-based Svelte 5 desktop application.

### Prerequisites and Tooling

Toolchain is managed by mise. Install mise using `curl https://mise.run | sh`. Install toolchain dependencies with `mise install`.

---

## Common Developer Commands

`task --list` shows developer commands. The most common tasks:

### CLI (Go) Development

- **Tidy Go modules**: `task cli:tidy`
- **Lint CLI**: `task cli:lint` and `task cli:lint:ci` (or `task cli:lint:fix` to apply fixes)
- **Format CLI**: `task cli:format`
- **Run unit tests**: `task cli:test` (writes coverage to `dist/profile.out`; a `dist` directory is required, e.g. `mkdir -p dist`)
- **Build development binary**: `task cli:build:dev` (output under `dist/devsy-dev_linux_amd64_v1/`)

### Desktop (Electron/Svelte 5) Development

- **Verify desktop code quality**: `task desktop:check` (Svelte check and TypeScript compiler)
- **Run desktop unit tests**: `task desktop:test`
- **Run desktop E2E (Playwright) tests**: `task desktop:test:e2e`

### Local Development: Agent Binary URL (`DEVSY_AGENT_URL`)

`DEVSY_AGENT_URL` overrides the URL the host downloads the agent binary from. By default the host fetches the agent binary from the published GitHub release; for local development testing, point it at a locally served binary instead.

Resolution order (`pkg/options/resolve.go`): the `DEVSY_AGENT_URL` environment variable, then the `AGENT_URL` context option, then the GitHub release default.

Setting `DEVSY_AGENT_URL` has two side effects in `pkg/agent/inject.go`: the host downloads the agent binary from the override URL (`PreferDownloadFromRemoteUrl = true`) and skips the remote version check (`SkipVersionCheck = true`), so a locally built development binary works without a matching released version.

Local setup:

```bash
# 1. Build the agent binary for the workspace's Linux arch (output: dist/devsy-dev_linux_amd64_v1/devsy-linux-amd64).
task cli:build:dev

# 2. Serve the built binary over HTTP.
mkdir -p bin
cp dist/devsy-dev_linux_amd64_v1/devsy-linux-amd64 bin/
python3 -m http.server 8080 --directory bin

# 3. Point Devsy at the local server.
export DEVSY_AGENT_URL=http://localhost:8080/
```

The agent binary runs inside the (Linux) workspace, so it is a Linux binary (`devsy-linux-amd64`) even when developing on macOS or Windows. The e2e suite uses the same pattern automatically through `framework.ServeAgent()` (`e2e/framework/server_utils.go`), which serves a `bin/` directory under `/files/` and sets `DEVSY_AGENT_URL` on non-Linux hosts.

---

## Testing and Headless Verification

### Headless / Xvfb Requirements

Desktop tests run inside an Electron browser environment. In headless or container environments, Electron commands require an `xvfb-run` prefix to emulate a display server:

```bash
xvfb-run task desktop:test # Desktop unit tests headlessly

xvfb-run task desktop:test:e2e # Desktop E2E tests headlessly
```

### E2E (Ginkgo) Tests

Devsy uses [Ginkgo](https://onsi.github.io/ginkgo/) for Go E2E and integration tests.

- **All E2E tests**: `task cli:test:e2e`
- **A focused test suite**: `task cli:test:e2e:suite -- "suite-name"`
- **A specific test pattern**: `task cli:test:e2e:focus -- "test-pattern"`

---

## Code Style and Quality

### Go Code Style

- **Idiomatic**: Focus on simplicity, reliability, and efficiency when writing clear, idiomatic Go code.
- **Style Guide**: Use the Uber style guide https://github.com/uber-go/guide/blob/master/style.md.
- **Linter**: `golangci-lint` via `task cli:lint` (or `task cli:lint:fix`). Run `task cli:lint:ci` before pushing changes.

### TypeScript / Svelte Code Style

Biome formats and checks web frontend files.

---

## Pull Request and Commit Guidelines

1. **Contributor License Agreement (CLA)**: All contributors sign the CLA.
2. **Commit messages**: Conventional Commits, with a concise subject line.
3. **Commit signing**: All commits are required to be signed.
4. **Branch name**: Branch should be named according to the task.
5. **Pre-commit checks**: Linters, checkers, and relevant unit tests run before pushing. `prek` (a pre-commit hook manager) manages them.
