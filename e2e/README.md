### E2E tests

#### Prerequisites

Make sure you have ginkgo installed on your local machine:
```
go get github.com/onsi/ginkgo/ginkgo
```

To build the binaries locally use the following command from this directory
```
BUILDDIR=bin SRCDIR=".." ../hack/build-e2e.sh
```

#### Kubernetes Tests Setup

For Kubernetes tests, Kind v0.33.0 or newer is required for the Kubernetes v1.36.4 node image. The setup task enforces this prerequisite automatically.

For tests that require Kubernetes (labeled with `up-kubernetes` or `build`), you need to set up a kind cluster:
```bash
kind create cluster --image kindest/node:v1.36.4@sha256:099e049362a1526b2db71494e1947aae99bd16290d7c895f2b7ea312e3cbfaed
```

To delete the cluster after testing:
```bash
kind delete cluster
```

#### Run all E2E test
```
# Install ginkgo and run in this directory
ginkgo
```

#### CI helpers

The `Windows Transport Tests` job runs native transport and runtime-helper unit
tests independently of CLI artifact builds. After that job and the builds pass,
the reusable `docker-buildkit-windows.yml` workflow runs the
`docker-internal-buildkit` regression specs. It provisions Docker Engine in WSL2,
reached over SSH, with `devsy-ci docker-wsl setup` (`e2e/cmd/devsy-ci`). The runtime
logic lives in `e2e/framework`:

| Package | Responsibility |
| --- | --- |
| `runenv` | Subprocess, filesystem, clock, network and download seams, plus the typed `StageError`. |
| `dockerwsl` | Provisions and tears down dockerd in WSL2, reached over SSH, for Windows runners. |

Their unit tests need no Docker, WSL or network:

```bash
go test ./e2e/framework/runenv/... ./e2e/framework/dockerwsl ./e2e/cmd/devsy-ci
```

Ginkgo executes the specs directly. `devsy-ci check-report` checks its JSON
report: all six BuildKit specs must pass on their first attempt, with no
selected skips, dry run or programmatic focus. Teardown runs on success and
failure, before the report and non-secret diagnostics are uploaded.
