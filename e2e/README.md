# E2E tests

End-to-end tests for the Devsy CLI, written with [Ginkgo](https://onsi.github.io/ginkgo/). Run all
commands below from the repository root.

## Prerequisites

- Go and [Task](https://taskfile.dev)
- Docker, for the Docker-based suites
- [kind](https://kind.sigs.k8s.io/) v0.33.0 or newer, for Kubernetes suites

The `task` commands run Ginkgo through `go tool ginkgo`, which uses the version
pinned in `go.mod`. To install a `ginkgo` binary on your `PATH`, run
`task cli:test:e2e:ginkgo:install`.

## Build

`task cli:test:e2e:build` builds Devsy and copies the Linux amd64 and host
binaries into `e2e/bin/`. The run tasks below do this automatically.

## Run

```bash
# all suites
task cli:test:e2e

# suites selected by label
task cli:test:e2e:suite -- up-docker-compose

# specs selected by name pattern
task cli:test:e2e:focus -- "pattern"
```

`cli:test:e2e:act:focus` runs a focused pattern through [act](https://github.com/nektos/act).

## Kubernetes tests

Specs labeled `up-provider-kubernetes` or `build` need a kind cluster. The setup task
checks the kind version and creates a cluster from
`kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed`:

```bash
task cli:test:e2e:kind:setup
```

Remove the cluster afterwards:

```bash
task cli:test:e2e:kind:teardown
```
