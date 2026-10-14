# MicroSandbox integration tests

Each capability has a Ginkgo label and scenario file. Parity suites run the same
contracts against the built-in and external providers. Keep scenario assertions in those files so
resource, ownership and lifecycle expectations remain visible.

## Shared setup

Use `microsandboxTestSession` for runtime prerequisites, isolated Devsy
configuration, the CLI framework, temporary workspaces and provider registration.
The shared provider selector supplies the pinned source, runtime identity and
provider label while callers retain their descriptive provider names.

Workspace cleanup registration is explicit. Ginkgo cleanup runs in reverse
order; register volume cleanup before workspace cleanup so the VM is torn down
first. Specialized fixtures own their volumes, registries, endpoints, processes
and environment overrides. Register independent cleanup callbacks when every
resource needs a cleanup attempt after failure.

The image, mount, resource, storage and recreation fixtures share session setup.
Other fixtures may adopt it when touched, preserving their existing resource
ownership and cleanup order. Do not put scenario switches or arbitrary cleanup
callbacks into the session.

## Continuous integration

The dedicated `integration-tests-microsandbox` job in
`.github/workflows/pr-ci.yml` owns the suite catalogue, runner configuration and
artifact uploads. The Go utility in `hack/microsandbox` installs and checks the
pinned Linux bundle and owns test execution, managed fixture selection, output
capture and report validation. The Linux build job supplies its CI-only executable;
it is not a Devsy release artifact. Each suite still gets a fresh runner, a
25-minute job limit and a 20-minute test limit, with one attempt. The checked
runtime bundle, build artifacts, required KVM gate
and credentials are configured once in the job definition.

Each matrix entry names its Ginkgo label and expected scenario count.
The helper's `verify-report` command requires a real successful run with that
exact count, split equally between the explicit `microsandbox-builtin` and
`microsandbox-external` labels by default. A contract specific to the published
provider uses an explicit `provider-mode: external` catalogue entry and requires
every selected case to carry only the external provider label. Missing cases,
skips, retries, dry runs and programmatic focus fail the gate. Reports and test
output are retained even when the test command fails.

Fixture and performance unit tests run by package in the existing unit-test jobs,
so additions do not require maintaining a second test-name list.

To add a scenario:

1. Extend the appropriate capability file using the shared provider selector.
2. Keep a focused scenario deadline and explicit expected behavior.
3. Update the existing suite's expected count in the catalogue.
4. Run that label locally and obtain actual supported-runtime CI evidence.

A new suite needs a catalogue entry only when it has a separate contract or
isolation requirement. Avoid copying runner setup or adding a job for each
individual assertion.

## Published-provider registry consumption

The Linux-only `up-provider-microsandbox-registry-consumer` suite exercises the
production external host's Runtime Protocol `RunImage` path with the installed,
checksum-verified provider. Its three external-only cases cover an owned TLS
registry through a restricted CONNECT proxy, refusal without the fixture CA,
and refusal without registry credentials. A private credential helper records
the direct parent executable digest and operation identity without recording
credentials. Successful image responses are checked against their actual body
digests and byte counts, and the positive case verifies a guest marker.

This contract calls `RunImage` directly because ordinary `up` resolves image
metadata before invoking the provider. It proves the published provider's image
consumer; ordinary workspace orchestration and remote agent binary preparation
have separate contracts. Each case owns fresh runtime state and an immutable
image reference. Registry seeding finishes before the measured listeners start,
and fixture shutdown joins active handlers before observations are inspected.

The previous 74-case native parity baseline remains evidence for those existing
rows only. This external-only row brings the configured catalogue to 77 cases;
the earlier 74-case run does not prove the new consumer cases or the expanded
catalogue. Acceptance requires fresh current-head native reports for all rows.

On Linux, the E2E test binary also serves as the private Docker credential helper.
Its `TestMain` preserves the production runtime-supervisor dispatch and recognizes
the helper only at its canonical, current-user-owned private path. Other test
invocations retain the normal test runner path.

## Existing-workspace source migration

The `up-provider-microsandbox-source-migration` external-only row covers explicit
`provider set-source` migration from the built-in MicroSandbox descriptor to the
published external provider, for both running and stopped workspaces. Each case
starts with a unique provider/workspace/volume and user-provided options. It
checks stored source and driver identity, successful activation, preserved
provider name/default/options, workspace and SSH records, VM creation time,
root data, named-volume data and a host bind marker.

The running case requires the same guest boot ID through migration and a later
`up`. The stopped case checks that migration leaves the VM stopped before any
command resumes it; subsequent `up` must start that same VM with its data intact.
Deletion must remove the migrated workspace record and the exact owned sandbox.
Independent cleanup attempts cover the workspace, sandbox fallback and volume.
The command-package unit test separately covers activation failure after a
committed replacement: it retains the replacement source and workspace data,
records the initialization error and preserves user options and default choice.

This source-prepared contract requires two actual native cases, each passed once
without selected skips. Compilation and dry-run registration are not migration
proof. Stored internal descriptors refresh executable blocks; they do not
implicitly migrate driver or binaries. A future bundled-default transition must
retain a literal legacy built-in fixture for these starting workspaces.

## Runtime startup measurements

The existing two-case lifecycle label optionally captures its ordinary fresh
and stop/resume `up` commands for the external provider when CI supplies all five
performance inputs: `DEVSY_TEST_RUNTIME_SPAWN_PROBE`,
`DEVSY_TEST_RUNTIME_SUPERVISOR`, `DEVSY_TEST_RUNTIME_PERFORMANCE_DIR`,
`DEVSY_TEST_RUNTIME_REVISION`, and `DEVSY_TEST_RUNTIME_SDK_VERSION`. The
built-in case retains its existing path. No new suite, VM, deadline or latency
threshold is added. After both commands succeed, the helper stages the published
runtime from `Agent.Binaries`, verifies its manifest checksum and compares direct
and supervised Info/Find launches in direct/supervised/supervised/direct order.
Each run records one first launch and 100 warm samples with process-reaping checks.
The same Go CI helper builds both tools through `build-performance-tools` from
the unreplaced Runtime SDK version in `go.mod`. The producer uploads the tools
and their SDK-version marker for native jobs to reuse. Their build metadata and
selected supervisor digest are verified before accepting reports.

The retained JSON includes the provider source, runtime digest, checkout revision,
SDK version, command wall time and filtered startup-event durations. Startup events
measure launch/handshake/dispense before RPC; they exclude binary resolution, RPC
and reaping and must not be described as total plugin overhead. The comparison
runs after both ordinary `up` captures so it does not prewarm their provider path.
It measures warm host caches, not a cold-cache or end-to-end speedup guarantee.
Raw probe stderr and environment values are not included in the performance artifact.
Normal suites remain inactive when these optional CI paths are unset.

## Managed policy boundary

Managed-policy recreation is Linux-only and runs through
`hack/run-managed-policy-e2e.sh`. It overlays `/etc` in a private mount namespace
before writing policy and validates namespace identity, owned overlay paths,
ownership and file modes. Ordinary suites never enter that fixture.

Both recreation suites use `safeRecreateFixture` for host-state snapshots and
replacement assertions. Inspect a rejected stopped VM before any command can
resume it. A later successful replacement must change the VM generation, clear
the old root marker, preserve external data and records, and expose the requested
resources.

See the runtime protocol documentation for supported runtime versions, individual
suite commands and the isolated managed-policy command. A dry run proves
selection only; it does not prove VM behavior.
