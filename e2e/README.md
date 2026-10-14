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

CI drives the suite through `devsy-ci` (`e2e/cmd/devsy-ci`), which provisions a
runtime, runs the compiled suite for one label, and verifies its Ginkgo report.
The logic lives in `e2e/framework`:

| Package | Responsibility |
| --- | --- |
| `runenv` | Subprocess, filesystem, clock, network and download seams, plus the typed `StageError`. |
| `specreport` | Validates a Ginkgo JSON report against the expected label and spec titles. |
| `e2erun` | Runs the test binary under a runtime profile and verifies the report. |
| `dockerwsl` | Provisions and tears down dockerd in WSL2, reached over SSH, for Windows runners. |

Their unit tests need no Docker, WSL or network:

```bash
go test ./e2e/framework/runenv/... ./e2e/framework/specreport \
  ./e2e/framework/e2erun ./e2e/framework/dockerwsl ./e2e/cmd/devsy-ci
```
