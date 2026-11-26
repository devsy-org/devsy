# E2E tests

End-to-end tests for the Devsy CLI, written with [Ginkgo](https://onsi.github.io/ginkgo/). Run all
commands from the repository root.

## Prerequisites

- Go and [mise](https://mise.jdx.dev)
- Docker, for the Docker-based suites
- [kind](https://kind.sigs.k8s.io/) v0.33.0 or newer, for Kubernetes suites

The `mise run` tasks use `go tool ginkgo`, which runs the version in `go.mod`. To install a
`ginkgo` binary on your `PATH`, run `mise run cli:test:e2e:ginkgo:install`.

## Build

`mise run cli:test:e2e:build` builds Devsy and copies the Linux and host binaries into
`e2e/bin/`. The run tasks below call it.

## Run

```bash
# all suites
mise run cli:test:e2e

# suites selected by label
mise run cli:test:e2e:suite up-docker-compose

# specs selected by name pattern
mise run cli:test:e2e:focus "pattern"
```

The `with env vars` workspace spec uses a local Docker fixture. Its repeated `up` calls test environment changes without waiting for remote repository inspection.

`cli:test:e2e:act:focus` runs a focused pattern through [act](https://github.com/nektos/act).

The Docker lifecycle regression covers values with `=` from `--workspace-env` and
`--workspace-env-file`, including restart and `remoteEnv` precedence:

```bash
mise run cli:test:e2e:focus "lifecycle hooks preserve equals signs"
```

To test local changes with Docker on macOS, run this from `e2e/` and set
`DEVSY_AGENT_BINARY` to the local Linux agent that matches the Docker architecture:

```bash
DEVSY_AGENT_BINARY="$PWD/bin/devsy-linux-arm64" \
  GOMAXPROCS=1 GOFLAGS=-p=1 mise exec -- go tool ginkgo --focus \
  "lifecycle hooks preserve equals signs"
```

`DEVSY_AGENT_BINARY` makes the container run your agent. Without it, the container can run a
cached release binary.

## Kubernetes tests

Specs labeled `up-provider-kubernetes` or `build` need a kind cluster. The setup task checks
the kind version, then creates a cluster from
`kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed`:

```bash
mise run cli:test:e2e:kind:setup
```

Remove the cluster afterwards:

```bash
mise run cli:test:e2e:kind:teardown
```

## CI test tiers

`hack/ci/suites.yaml` defines every test tier that `.github/workflows/pr-ci.yml` runs. See
`hack/ci/README.md`. To run a new Ginkgo label in CI, add it to `e2e.suites` there.
`go test ./hack/ci` fails while a label in this directory has no entry.

### CI helpers

The `Unit Tests (windows-latest)` job runs the Windows transport and runtime-helper unit
tests. It does not depend on CLI artifact builds. After the CLI builds pass, the
`docker-internal-buildkit` suite (runtime `docker-wsl` in `hack/ci/suites.yaml`) runs the
BuildKit regression specs. Its job sets up Docker Engine in WSL2 over SSH with
`devsy-ci docker-wsl setup` (`e2e/cmd/devsy-ci`). The runtime logic is in these packages
under `e2e/framework`:

| Package | Responsibility |
| --- | --- |
| `runenv` | Subprocess, filesystem, clock, network and download seams, plus the typed `StageError`. |
| `dockerwsl` | Provisions and tears down dockerd in WSL2, reached over SSH, for Windows runners. |

These unit tests need no Docker, WSL, or network:

```bash
go test ./e2e/framework/runenv/... ./e2e/framework/dockerwsl ./e2e/framework/ginkgoreport ./e2e/cmd/devsy-ci
```

Ginkgo runs the specs directly. `ci e2e run` (or `devsy-ci check-report`) checks the JSON
report with `e2e/framework/ginkgoreport`. All six BuildKit specs must pass on the first
attempt, with no skipped specs, dry run, or programmatic focus. Teardown runs on success
and failure, before the report and non-secret diagnostics upload.
