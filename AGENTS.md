# AGENTS.md

Guide for AI coding agents working in the Devsy repository. It covers the codebase layout, environment setup, common commands, testing, code style, and contribution conventions.

---

## Environment Setup

Devsy is a monorepo with a Go-based CLI and an Electron-based Svelte 5 desktop application.

### Setup Target

A custom task installs all prerequisites and builds required artifacts in one step:

```bash
task setup:agent
```

This task:
1. Installs system dependencies (`protobuf-compiler`, `xvfb`) when `apt-get` is available (requires root or sudo privileges).
2. Installs the `goreleaser/v2` Go release tool.
3. Tidies Go modules (`task cli:tidy`).
4. Builds gRPC code (`task cli:build:grpc`, under `pkg/agent/tunnel`).
5. Sets up the Electron workspace and installs Node dependencies (`task desktop:setup`).

### Prerequisites and Tooling

Manual installation, when needed:

- **Go 1.26.3**: The repository targets Go 1.26.3 (`go.mod`). With `GOTOOLCHAIN=auto`, an older Go toolchain automatically downloads Go 1.26 when task commands run.
- **NodeJS 24**: Required for building and testing the desktop workspace (`.nvmrc`, CI `node-version`).
- **Taskfile (go-task)**: All build, test, and setup commands run through `task`. Installation:

  ```bash
  sudo sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin
  ```

---

## Common Developer Commands

`task --list` shows all available commands. The most common tasks:

### CLI (Go) Development

- **Tidy Go modules**: `task cli:tidy`
- **Lint CLI**: `task cli:lint` (or `task cli:lint:fix` to apply fixes)
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

Desktop tests run inside an Electron browser environment. In headless or container environments (CI, automated sandbox agents), Electron-dependent commands require an `xvfb-run` prefix to emulate a display server:

```bash
# Desktop unit tests headlessly
xvfb-run task desktop:test

# Desktop E2E tests headlessly
xvfb-run task desktop:test:e2e
```

### E2E (Ginkgo) Tests

Devsy uses [Ginkgo](https://onsi.github.io/ginkgo/) for Go E2E and integration tests.

- **All E2E tests**: `task cli:test:e2e`
- **A focused test suite**: `task cli:test:e2e:suite -- "suite-name"`
- **A specific test pattern**: `task cli:test:e2e:focus -- "test-pattern"`

---

## Code Style and Quality

### Go Code Style

- **Linter**: `golangci-lint` via `task cli:lint` (or `task cli:lint:fix`).
- **Logs**: Log messages and logging strings are lowercase.

### TypeScript / Svelte Code Style

Biome formats and checks web frontend files.

---

## Pull Request and Commit Guidelines

1. **Contributor License Agreement (CLA)**: All contributors sign the CLA. A CLA bot posts instructions on the PR after it is opened.
2. **Commit messages**: Conventional Commits, with a concise subject line (50 characters max).
3. **Commit signing**: Commits are signed by the Devsy GitHub App. Push or create commits through the app installation (see [GitHub App Authentication](#github-app-authentication)); GitHub signs commits made through the app.
4. **Pre-commit checks**: Linters, checkers, and relevant unit tests run before pushing. `prek` (a fast pre-commit hook manager) manages them.
   - Installation (Linux and macOS):

     ```bash
     curl --proto '=https' --tlsv1.2 -LsSf https://github.com/j178/prek/releases/latest/download/prek-installer.sh | sh
     ```

   - Installation (Windows):

     ```powershell
     powershell -ExecutionPolicy ByPass -c "irm https://github.com/j178/prek/releases/latest/download/prek-installer.ps1 | iex"
     ```

   - Manual run on all files:

     ```bash
     prek run --all-files
     ```

   - Git hook installation:

     ```bash
     prek install
     ```

## GitHub App Authentication

The release, CLA, and CI workflows authenticate as the Devsy GitHub App through a signed JWT. Local generation, after setting the app credentials:

```bash
export DEVSY_GITHUB_APP_ID=<app-client-id>
export DEVSY_GITHUB_APP_PRIVATE_KEY=$(cat path/to/private-key.pem)
task github:app:jwt
```

`task github:app:jwt` runs `hack/gen_github_app_jwt`, which produces an RS256-signed JWT with `iss`/`iat`/`exp` claims matching GitHub's requirements. The `iat` claim is backdated 60 seconds to tolerate clock drift, while `exp` expires 10 minutes after JWT generation (GitHub's maximum). The private key can also be provided through `DEVSY_GITHUB_APP_PRIVATE_KEY_PATH` (a PEM file path) or the `--app-id`/`--private-key`/`--private-key-content` flags.

### Discovering the App ID

When `DEVSY_GITHUB_APP_ID` is unavailable, discover it by listing installations for the organization. Authenticate with an existing app JWT (or any token with read access):

```bash
curl -H "Authorization: Bearer <your-token>" \
     -H "Accept: application/vnd.github+json" \
     https://api.github.com/orgs/{org}/installations
```

The response includes the `app_id` and `app_slug`:

```json
{
  "id": 555555,
  "app_id": 123456,
  "app_slug": "example-app",
  "target_id": 98765,
  "target_type": "Organization"
}
```

Use the returned `app_id` as the `iss` claim when minting an installation token from the private key. To confirm a private key matches an app, sign a JWT with `iss=<app_id>` and call `GET /app` — a 200 confirms the pairing.
