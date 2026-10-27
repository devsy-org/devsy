# E2E tests

End-to-end tests for the Devsy CLI, written with [Ginkgo](https://onsi.github.io/ginkgo/). Run all
commands below from the repository root.

## Prerequisites

- Go and [mise](https://mise.jdx.dev)
- Docker, for the Docker-based suites
- [kind](https://kind.sigs.k8s.io/) v0.33.0 or newer, for Kubernetes suites

The `mise run` tasks run Ginkgo through `go tool ginkgo`, which uses the version
pinned in `go.mod`. To install a `ginkgo` binary on your `PATH`, run
`mise run cli:test:e2e:ginkgo:install`.

## Build

`mise run cli:test:e2e:build` builds Devsy and copies the Linux amd64 and host
binaries into `e2e/bin/`. The run tasks below do this automatically.

## Run

```bash
# all suites
mise run cli:test:e2e

# suites selected by label
mise run cli:test:e2e:suite up-docker-compose

# specs selected by name pattern
mise run cli:test:e2e:focus "pattern"
```

`cli:test:e2e:act:focus` runs a focused pattern through [act](https://github.com/nektos/act).

## Kubernetes tests

Specs labeled `up-provider-kubernetes` or `build` need a kind cluster. The setup task
checks the kind version and creates a cluster from
`kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed`:

```bash
mise run cli:test:e2e:kind:setup
```

Remove the cluster afterwards:

```bash
mise run cli:test:e2e:kind:teardown
```

## CI test tiers

`hack/ci/suites.yaml` defines every test tier that `.github/workflows/pr-ci.yml` runs;
see `hack/ci/README.md`. To make a new Ginkgo label run in CI, add it to
`e2e.suites` there. `go test ./hack/ci` fails while a label in this directory has no
entry.

#### CI helpers

Native Windows transport and runtime-helper unit tests run in the `Unit Tests
(windows-latest)` job, independently of CLI artifact builds. After the CLI builds
pass, the `docker-internal-buildkit` suite of the integration group (runtime
`docker-wsl` in `hack/ci/suites.yaml`) runs the BuildKit regression specs. Its job
provisions Docker Engine in WSL2, reached over SSH, with `devsy-ci docker-wsl setup`
(`e2e/cmd/devsy-ci`). The runtime logic lives in `e2e/framework`:

| Package | Responsibility |
| --- | --- |
| `runenv` | Subprocess, filesystem, clock, network and download seams, plus the typed `StageError`. |
| `dockerwsl` | Provisions and tears down dockerd in WSL2, reached over SSH, for Windows runners. |

Their unit tests need no Docker, WSL or network:

```bash
go test ./e2e/framework/runenv/... ./e2e/framework/dockerwsl ./e2e/framework/ginkgoreport ./e2e/cmd/devsy-ci
```

Ginkgo executes the specs directly. `ci e2e run` (or `devsy-ci check-report`) checks the JSON
report through `e2e/framework/ginkgoreport`: all six BuildKit specs must pass on their first attempt, with no
selected skips, dry run or programmatic focus. Teardown runs on success and
failure, before the report and non-secret diagnostics are uploaded.

#### Native Windows T0 diagnostics

The branch-local `native-t0-qualification` job runs only when the workflow is dispatched with `t0_qualification=true`. Its evidence is an observation, not proof of editor lifetime unless the actual GUI, process and SSH acceptance criteria are met.

The PowerShell observer treats captured streams as untrusted. If a tail read would start at a nonzero file offset, it emits no stream text and records the byte count, truncation state and `contextless-tail-omitted` reason. Safe structured task errors are projected separately, so omitting a raw tail does not discard an independently available failure cause.

The WSL helper samples at most eight complete lines of at most 1 KiB from each daemon log; PEM blocks and overlong lines are omitted. It redacts the captured output and caps each sidecar append at 20 KiB of diagnostic text plus bounded phase metadata. Repeated appends can make the sidecar larger than 20 KiB. The sidecar requests the state file mode (`0o600`); this does not prove Windows ACL enforcement. The runner evidence file's ACLs also require native verification. Never place credentials, private keys or full SSH configurations in evidence.
