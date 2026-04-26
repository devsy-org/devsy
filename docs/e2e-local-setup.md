# E2E Tests — Local Setup

## Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| Go | 1.25.8+ | Compile test binary and test framework |
| Docker | Running daemon | DinD-based machineprovider and `devsy up` tests |
| [go-task](https://taskfile.dev) | Latest | Task runner (`task` CLI) |
| [act](https://github.com/nektos/act) | Latest | Local GitHub Actions simulation |
| [kind](https://kind.sigs.k8s.io) | Latest | Local Kubernetes cluster (k8s test suites only) |
| [goreleaser](https://goreleaser.com) | v2.x | Build the devsy binary |

Verify each tool is installed:

```bash
go version          # go1.25.8 or higher
docker version      # daemon must be running
task --version
act --version
kind version        # only needed for Kubernetes suites
goreleaser --version
```

## Environment Setup

1. Copy `.env.example` to `.env` at the repo root:

```bash
cp .env.example .env
```

2. Fill in the required values:

| Variable | Required | Description |
|----------|----------|-------------|
| `GH_USERNAME` | Yes | GitHub username (repository owner or personal account) |
| `GH_ACCESS_TOKEN` | Yes | GitHub PAT or App token with `repo` scope |
| `GH_CREDENTIAL_USERNAME` | Yes | `x-access-token` for App tokens, or your `GH_USERNAME` for PATs |
| `KUBECONFIG` | For k8s tests | Path to kubeconfig; defaults to `~/.kube/config` |
| `DOCKER_HOST` | No | Custom Docker socket (e.g. for Podman) |
| `DEVSY_AGENT_URL` | No | Agent binary URL; auto-set on non-Linux (see [Non-Linux note](#non-linux-agent-server)) |

3. Source the file before running tests (or export the vars in your shell):

```bash
set -a && source .env && set +a
```

## Running Tests

All targets live in `Taskfile.yml`. The build step (`cli:test:e2e:build`) compiles the devsy binary into `e2e/bin/devsy-linux-amd64` via goreleaser before tests run.

### Run all E2E tests

```bash
task cli:test:e2e
```

This builds the test binary (if missing), installs Ginkgo, then runs the full suite from the `e2e/` directory.

### Run focused tests by pattern

```bash
task cli:test:e2e:focus -- "machineprovider"
```

Passes `--focus <pattern>` to Ginkgo. Matches against `Describe`/`It` block names.

### Run a labeled test suite

```bash
task cli:test:e2e:suite -- "integration"
```

Passes `--label-filter <name>` to Ginkgo.

### Run via act (local CI simulation)

```bash
task cli:test:e2e:act:focus -- "up"
```

Executes the `.github/workflows/act.yml` workflow locally through `act`, targeting the `e2e-test` job with an optional focus pattern.

### Build the test binary only

```bash
task cli:test:e2e:build
```

Copies the goreleaser dev build to `e2e/bin/devsy-linux-amd64`. Skips if the binary already exists.

### Full target reference

| Target | Description |
|--------|-------------|
| `cli:test:e2e` | Build + run all E2E tests |
| `cli:test:e2e:focus -- "<pattern>"` | Run tests matching a Ginkgo `--focus` pattern |
| `cli:test:e2e:suite -- "<label>"` | Run tests matching a Ginkgo `--label-filter` |
| `cli:test:e2e:act:focus -- "<pattern>"` | Run tests via act with optional focus |
| `cli:test:e2e:build` | Build the test binary |
| `cli:test:e2e:kind:setup` | Create a kind cluster (`kindest/node:v1.34.0`) |
| `cli:test:e2e:kind:teardown` | Delete the kind cluster |
| `cli:test:e2e:ginkgo:install` | Install the Ginkgo CLI (`go install`) |

## Kubernetes Test Setup

Suites that test Kubernetes-based features require a running kind cluster.

```bash
task cli:test:e2e:kind:setup      # creates cluster with kindest/node:v1.34.0
# ... run tests ...
task cli:test:e2e:kind:teardown   # deletes the cluster
```

Ensure `KUBECONFIG` points to the kind-generated kubeconfig (kind writes to `~/.kube/config` by default).

## Test Suites

The E2E suite registers 13 test packages from `e2e/tests/`:

`build`, `context`, `dockerinstall`, `ide`, `integration`, `machine`, `machineprovider`, `provider`, `ssh`, `up`, `up-docker-compose`, `up-features`, `upgrade`

The `up-docker-compose` suite only compiles on Linux, Darwin, and Unix platforms (build-tagged).

## Non-Linux Agent Server

On non-Linux platforms (macOS, Windows), the test framework automatically starts an HTTP file server that serves the compiled agent binary from `e2e/bin/`. It binds to a local network interface on a random port and sets `DEVSY_AGENT_URL` to point at it. The test suite waits up to 30 seconds for this server to become available.

Override this by setting `DEVSY_AGENT_URL` in your `.env` to point at a custom agent binary location.

## Known Limitations

- **act timeouts.** `devsy up` can timeout during local act runs even on good commits. The act runner has higher overhead than direct Ginkgo execution; prefer `task cli:test:e2e` or `task cli:test:e2e:focus` for faster iteration.
- **Docker daemon required.** Several suites (`machineprovider`, `up`, `up-docker-compose`) need a running Docker daemon with DinD support.
- **Test repos need `devcontainer.json`.** Private-repo tests expect the target repo to contain a `.devcontainer.json` to avoid MCR (Microsoft Container Registry) rate limits.

## Troubleshooting

**`test -f e2e/bin/devsy-linux-amd64` fails**
The build step requires goreleaser. Run `task cli:build:dev` manually and check for goreleaser errors.

**Ginkgo not found**
Run `task cli:test:e2e:ginkgo:install` or `go install github.com/onsi/ginkgo/v2/ginkgo@latest` directly.

**Timeout waiting for `DEVSY_AGENT_URL`**
Non-Linux only. The agent HTTP server failed to bind. Check that no firewall blocks local TCP connections, and that the `e2e/bin/` directory contains the compiled binary.

**Kind cluster not reachable**
Verify `KUBECONFIG` points to the kind config. Run `kubectl cluster-info` to confirm connectivity. Tear down and recreate if stale: `task cli:test:e2e:kind:teardown && task cli:test:e2e:kind:setup`.

**GitHub authentication errors**
Confirm `GH_ACCESS_TOKEN` has `repo` scope. For GitHub App tokens, `GH_CREDENTIAL_USERNAME` must be `x-access-token`.
