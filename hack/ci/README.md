# hack/ci

Go implementation of the scripting behind `.github/workflows/pr-ci.yml`. The
workflow only wires GitHub Actions together (checkout, caches, artifacts). What
tests exist, where they run and how they run is defined here.

## Test tiers

[`suites.yaml`](suites.yaml) is the single manifest.

| Tier | Contents | Runs | Defined by |
| --- | --- | --- | --- |
| `unit` | Go tests that need no running workspace | `go run ./hack/ci unit` (also `mise run cli:test`) | `unit.steps` |
| `e2e`, group `unprivileged` | Ginkgo suites run as the runner user | `ci e2e run` | `e2e.suites` |
| `e2e`, group `integration` | Ginkgo suites run with sudo on Linux, on Podman-backed Windows, or on rootless/rootful Podman | `ci e2e run` | `e2e.suites` |
| `e2e`, group `microsandbox` | MicroSandbox suites run through `devsy-ci-microsandbox` | `devsy-ci-microsandbox run` | `e2e.suites` |

All unit tests run together: `ci unit` runs every step of the manifest that
applies to the current OS, and the `Unit Tests` job runs it on each runner under
`unit.runners`. All integration suites run as one block of jobs generated from
`e2e.suites`.

## Adding tests

- **Go unit test in an ordinary package**: nothing to do. Every package outside
  `test/` and `e2e/` directories is discovered.
- **Go test under `e2e/` or `hack/` that must run as a unit test**: add the
  package to a `unit.steps` entry (or to `unit.excluded` with a reason).
- **Ginkgo spec**: add its label to `e2e.suites`, choosing a `group`. Set only
  what differs from the group defaults (`runner`, `test_timeout`,
  `install_kind`, `install_podman`, `requires_secret`, `host_setup`, ...). Labels
  that another workflow runs go to `e2e.ungated` with a reason.

`go test ./hack/ci` enforces both rules: it fails when a Ginkgo label or a Go test
package is not assigned to a tier, and when the manifest names something that no
longer exists. The workflow needs no edits for new tests.

## Commands

| Command | Purpose |
| --- | --- |
| `ci matrix [--group G] [--secret-available]` | Print the job matrices as GitHub outputs. Suites that need the GitHub App secret are dropped when it is unavailable. |
| `ci unit [--os GOOS] [--step NAME] [--list] [--coverprofile P]` | Run the unit tier on this machine. |
| `ci e2e prepare --bin-src DIR --e2e-dir DIR` | Stage the CLI binaries next to `e2e.test`. |
| `ci e2e run --label L --runner R` | Run a suite: focused checks, sudo and environment handling, post checks. |
| `ci setup host --label L --runner R` | Run the suite's `host_setup` actions (`remove-docker`, `insecure-registry`). |
| `ci setup podman-linux ...` | Download, verify and install the pinned static Podman build. |
| `ci success --always-run JOBS` | Fold the `needs` results into the required `CI Success` check. |

The Podman and MicroSandbox runtime helpers stay in `hack/podman_ci` and
`hack/microsandbox`.
