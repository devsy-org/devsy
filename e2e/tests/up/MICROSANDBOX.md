# MicroSandbox integration tests

Each capability has a Ginkgo label and scenario file. The built-in and external
providers run the same contracts. Keep scenario assertions in those files so
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
it is not a Devsy release artifact. Each suite still gets a fresh runner, a 25-minute job limit and a 20-minute test limit,
with one attempt. The checked runtime bundle, build artifacts, required KVM gate
and credentials are configured once in the job definition.

Each matrix entry names its Ginkgo label and expected scenario count.
The helper's `verify-report` command requires a real successful run with that
exact count, split equally between the explicit `microsandbox-builtin` and
`microsandbox-external` labels. Missing cases, skips, retries, dry runs and
programmatic focus fail the gate. Reports and test output are retained even when
the test command fails.

To add a scenario:

1. Extend the appropriate capability file using the shared provider selector.
2. Keep a focused scenario deadline and explicit expected behavior.
3. Update the existing suite's expected count in the catalogue.
4. Run that label locally and obtain actual supported-runtime CI evidence.

A new suite needs a catalogue entry only when it has a separate contract or
isolation requirement. Avoid copying runner setup or adding a job for each
individual assertion.

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
