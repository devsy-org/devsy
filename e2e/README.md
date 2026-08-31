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
kind create cluster --image kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5
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
