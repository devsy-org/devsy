# hack/ci

Go code that runs the PR workflow's tests. `.github/workflows/pr-ci.yml` only
connects GitHub Actions steps: checkout, caches, and artifacts. The manifest
and subcommands here decide which tests exist, where they run, and how.

## Test tiers

[`suites.yaml`](suites.yaml) is the only manifest.

| Tier | Contents | Runs | Defined by |
| --- | --- | --- | --- |
| `unit` | Go tests that need no running workspace | `go run ./hack/ci unit` or `mise run cli:test` | `unit.steps` |
| `e2e`, group `unprivileged` | Ginkgo suites that run as the runner user | `ci e2e run` | `e2e.suites` |
| `e2e`, group `integration` | Ginkgo suites that need a container runtime. Linux runs them with sudo unless a suite sets `privileged: false`. | `ci e2e run` | `e2e.suites` |
| `e2e`, group `microsandbox` | MicroSandbox suites | `devsy-ci-microsandbox run` | `e2e.suites` |

`ci unit` runs every step that applies to the current OS. The `Unit Tests` job
runs it on each runner in `unit.runners`. The workflow builds one job matrix per
e2e group from `e2e.suites`.

## Adding tests

- **Go unit test in an ordinary package**: no change. The `packages` step finds
  every package outside `test/` and `e2e/` on Linux and macOS.
- **Go test under `test/` or `e2e/` that must run as a unit test**: add its
  package to a `unit.steps` entry. List packages that stay out in
  `unit.excluded` with a reason.
- **Go test that must run on Windows**: discovery skips Windows. List its
  packages in a `unit.steps` entry with `os: [windows]`.
- **Ginkgo spec**: add its label to `e2e.suites` and choose a `group`. Set only
  the fields that differ from the group defaults, such as `runner`,
  `test_timeout`, `install_kind`, `install_podman`, `requires_secret`, and
  `host_setup`. List labels that another workflow runs in `e2e.ungated` with a
  reason.

`go test ./hack/ci` fails when a Ginkgo label or Go test package has no tier, or
when the manifest names a label that no longer exists. New tests need no
workflow edits.

## Commands

| Command | Purpose |
| --- | --- |
| `ci matrix [--group G] [--secret-available]` | Print the job matrices as GitHub outputs. Drops suites that need the GitHub App secret when it is unavailable. |
| `ci unit [--os GOOS] [--step NAME] [--list] [--coverprofile P]` | Run the unit tier on this machine. |
| `ci e2e prepare --bin-src DIR --e2e-dir DIR` | Put the CLI binaries next to `e2e.test`. |
| `ci e2e run --label L --runner R` | Run a suite, with its focused checks, sudo and environment setup, and post checks. |
| `ci setup host --label L --runner R` | Run the suite's `host_setup` actions: `remove-docker` and `insecure-registry`. |
| `ci setup podman-linux ...` | Download, verify, and install the pinned static Podman build. |
| `ci success --always-run JOBS` | Fold the `needs` results into the required `CI Success` check. |

The Podman and MicroSandbox runtime helpers live in `hack/podman_ci` and
`hack/microsandbox`.
