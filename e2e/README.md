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
