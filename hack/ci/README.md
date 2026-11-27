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

## Change selection

Pushes to main and manual workflow runs use `--mode=full`. They run every
suite whose required secret is available and build Linux, macOS, and Windows
binaries.

A push to main can skip the Go, e2e, and microsandbox jobs. `ci scope` compares
the pushed commit with the previous main commit. When that diff is one commit
and every changed file matches `selection.light_run_paths` and none matches
`selection.light_run_excludes`, the `changes` job reports every path output
as false. The previous main commit must also have a finished, uncancelled run
(`success` or `failure`), because pushes to main share one concurrency group
and a newer push replaces an older run that is still waiting. Anything else,
including a force push, a push of several commits, a missing previous SHA, a
cancelled previous run, a git or API error, runs in full. The excludes list
files a Go test or a CI job reads (`install.sh`, the microsandbox docs page, `AGENTS.md`,
the licenses file) and Markdown inside the Go trees. Scheduled runs (nightly)
and manual runs always run in full. Pre-commit still runs on every push.

GitHub does not promise that a waiting main run is replaced in push order, so
an older push's run can replace the run of the newest commit. `ci ensure-tip
--sha SHA` runs in the `Main Tip Run` job of every push run. When SHA is not
main's tip and the tip has no run that was not cancelled, it starts a manual
run for the tip, which always runs in full. Any run that does start repeats the
check, so the tip is covered whatever the order was.

Pull requests use `--mode=pr --changed-files FILE`. The file contains one
repository-relative path per line. Paths in `selection.full_run_paths` select
every suite. Paths in `selection.core_paths` select the 13 suites marked
`pr_smoke` plus suites with a matching `paths` entry. Other changes select
only suites with a matching entry. Patterns support `*` within one path
segment and `**` across zero or more segments.

Changes under `e2e/cmd/` are shared CI dependencies and select the full matrix.

The build matrix follows selected suites. Every E2E run needs the Linux
binary, including Windows runs that inject it into containers. Windows
suites also need a Windows build. MicroSandbox suites enable the provider
build on Linux. Full-run paths retain all three build runners. Unmatched
changes produce empty E2E and build matrices. Unit test runners are unchanged.

`ci success` accepts conditional skips only when successful change detection
and planning provide valid outputs that explain them. Missing or failed
planning, invalid outputs, and unexpectedly skipped required jobs fail the
gate.

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
  `host_setup`. Add `paths` for the suite's test files and product dependencies.
  Set `pr_smoke` only for the shared core smoke tier. List labels that PR CI
  must not select directly in
  `e2e.ungated` with a reason. These include sub-labels exercised by gated
  suites and labels run manually or by another workflow.

Keep suite paths aligned with implementation packages when code moves.
Include runtime helpers such as `pkg/dockerinstall/**`, not only provider wrappers.
Include direct compile dependencies in native host job filters.

`go test ./hack/ci` fails when a Ginkgo label or Go test package has no tier, or
when the manifest names a label that no longer exists. New tests need no
workflow edits.

## Commands

| Command | Purpose |
| --- | --- |
| `ci matrix [--group G] [--secret-available] [--mode full\|pr] [--changed-files FILE]` | Print unit, build, and E2E job matrices as GitHub outputs. Apply change selection and secret availability. |
| `ci unit [--os GOOS] [--step NAME] [--list] [--coverprofile P]` | Run the unit tier on this machine. |
| `ci e2e prepare --bin-src DIR --e2e-dir DIR` | Put the CLI binaries next to `e2e.test`. |
| `ci e2e run --label L --runner R` | Run a suite, with its focused checks, sudo and environment setup, and post checks. |
| `ci setup host --label L --runner R` | Run the suite's `host_setup` actions: `remove-docker` and `insecure-registry`. |
| `ci setup podman-linux ...` | Download, verify, and install the pinned static Podman build. |
| `ci success --always-run JOBS` | Fold the `needs` results into the required `CI Success` check. |
| `ci rerun-no-runner --run-id ID [--ignore JOBS] [--dry-run]` | Rerun the failed jobs of a run's first attempt once when every failure is a macOS job GitHub cancelled before assigning a runner, or a Windows job whose log shows a Go runtime fatal error (`fatal error:` or `0xc0000005`) and `XTILEDATA=True`. |
| `ci cache-prune [--week YYYY-Www] [--dry-run]` | Delete the caches of closed pull requests, and main's `go-unit` caches from earlier weeks once a compatible cache exists for the current week: same OS, architecture, Go version and GitHub cache version. |

The Podman and MicroSandbox runtime helpers live in `hack/podman_ci` and
`hack/microsandbox`.
