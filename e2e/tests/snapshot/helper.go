package snapshot

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
)

// registryHostPort is a fixed host port for the local registry fixture,
// rather than an ephemeral one (`-p 0:5000`): the Docker daemon's
// "insecure-registries" allowlist (see below) must name an exact host:port,
// with no wildcard/range syntax, so the port has to be known ahead of time.
// Fixed is safe here because specs in this file run serially, never two
// registries at once.
const registryHostPort = "15500"

// registryImage pins registry:2 by digest, matching the busybox@sha256:...
// pinning convention already used elsewhere in the e2e/CI workflows, so a
// new tag push upstream can't change this fixture's behavior underfoot.
const registryImage = "registry@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373"

// registryHost is the address e2e specs push/pull snapshots against.
//
// host.docker.internal rather than localhost: `snapshot create`'s
// push-permission check and registry push run on the host CLI process
// itself (before any container exists), but `snapshot restore`'s volume
// pull runs from the agent process *inside* the newly-created container
// (see cmd/internal/agentcontainer/setup.go), where "localhost" resolves to
// the container's own loopback, not the host's. host.docker.internal must
// resolve identically from both places:
//   - Docker Desktop/OrbStack provide this automatically for both. Plain
//     Linux (this fixture's actual CI target) provides it for neither:
//     the host CLI process needs an /etc/hosts entry pointing it at
//     127.0.0.1 (the registry's published port), and the container needs
//     the explicit "--add-host=host.docker.internal:host-gateway" runArgs
//     entry the fixture devcontainer.json under tests/snapshot/testdata/docker
//     carries (host-gateway resolves to the docker0 bridge IP, which also
//     reaches that published port). Both are CI/environment prerequisites
//     this fixture cannot configure for itself.
//
// This also requires two things outside this file:
//   - snapshot.go's BeforeEach sets DEVSY_INSECURE_DOCKER_INTERNAL=true on
//     this process, inherited by the devsy CLI subprocesses it spawns, so
//     pkg/snapshot's reference parsing marks host.docker.internal refs as
//     name.Insecure (plain HTTP instead of HTTPS) for this suite only —
//     off by default everywhere else, since a hostname alone isn't proof a
//     registry is actually local.
//   - the Docker *daemon* itself must also be configured to allow plain HTTP
//     to this address for `docker commit`/`docker push` (used by
//     CommitContainer/PushDevContainer), via
//     /etc/docker/daemon.json: {"insecure-registries":
//     ["host.docker.internal:15500"]}, followed by a daemon restart. This is
//     an environment/CI prerequisite this fixture cannot configure for
//     itself (it would need root and to restart the daemon out from under
//     any other running containers) — the CI workflow must set this up
//     before running this suite, alongside the /etc/hosts entry above.
var registryHost = "host.docker.internal:" + registryHostPort

// startLocalRegistry runs a disposable registryImage container bound to
// registryHostPort, mirroring the manual `docker run` pattern already used
// for fixture containers in tests/up/provider_docker.go. Returns the
// "host:port" address to push/pull against and a cleanup func. Deletes are
// enabled so e2e specs can exercise DeleteManifest against a real registry.
func startLocalRegistry(
	ctx context.Context, dockerHelper *docker.DockerHelper,
) (string, func(), error) {
	id, err := runRegistryContainerWithRetry(ctx, dockerHelper)
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		// Deliberately not ctx: cleanup runs from AfterEach/DeferCleanup after
		// the spec's own context may already be cancelled (e.g. on failure),
		// and a leaked registry container would block the next spec from
		// binding the same fixed host port.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = dockerHelper.Stop(cleanupCtx, id)
		_ = dockerHelper.Remove(cleanupCtx, id)
	}

	return registryHost, cleanup, nil
}

// registryStartAttempts bounds retries for a transient `docker run` failure
// immediately after the CI workflow's `systemctl restart docker`: `docker
// info` reporting the daemon ready doesn't guarantee its container-creation
// subsystem has finished warming up, so the very first `docker run` in a job
// can fail with exit 125 moments after the daemon restart succeeds.
const registryStartAttempts = 3

// runRegistryContainerWithRetry runs registryImage, retrying a
// handful of times on failure. Returns the started container's ID.
//
// The container ID is captured directly off `docker run -d`'s stdout rather
// than looked up afterwards by a shared static label: a leaked registry
// container from a previous (e.g. failed) spec would otherwise make a
// label-based lookup ambiguous or match the wrong container.
// registryLabels identifies the disposable registry fixture container so a
// leaked one from a previous crashed/timed-out run can be found and removed.
var registryLabels = []string{"devsy-e2e-snapshot-registry=true"}

// removeLeakedRegistryContainers is best-effort: a leaked container would
// otherwise permanently hold registryHostPort and fail every subsequent
// attempt with a confusing port-already-allocated error instead of the real
// cause.
func removeLeakedRegistryContainers(ctx context.Context, dockerHelper *docker.DockerHelper) {
	leaked, err := dockerHelper.FindContainer(ctx, registryLabels)
	if err != nil {
		return
	}
	for _, id := range leaked {
		_ = dockerHelper.Stop(ctx, id)
		_ = dockerHelper.Remove(ctx, id)
	}
}

// startRegistryContainer runs a single attempt at starting registryImage and
// returns the started container's ID captured directly off `docker run -d`'s
// stdout, rather than looked up afterwards by registryLabels: a leaked
// registry container from a previous (e.g. failed) spec would otherwise make
// a label-based lookup ambiguous or match the wrong container.
func startRegistryContainer(
	ctx context.Context, dockerHelper *docker.DockerHelper,
) (string, error) {
	var stdout, stderr bytes.Buffer
	err := dockerHelper.Run(ctx, []string{
		"run", "-d",
		"--label", registryLabels[0],
		"-e", "REGISTRY_STORAGE_DELETE_ENABLED=true",
		"-p", registryHostPort + ":5000",
		registryImage,
	}, docker.Streams{Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		return "", fmt.Errorf(
			"start local registry: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()),
		)
	}
	id := strings.TrimSpace(stdout.String())
	if id == "" {
		return "", fmt.Errorf("start local registry: docker run produced no container id")
	}
	return id, nil
}

// runRegistryContainerWithRetry runs registryImage, retrying a handful of
// times on failure. Between attempts it re-sweeps for containers leaked by
// the previous attempt, not just the ones from before the loop started.
func runRegistryContainerWithRetry(
	ctx context.Context, dockerHelper *docker.DockerHelper,
) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= registryStartAttempts; attempt++ {
		removeLeakedRegistryContainers(ctx, dockerHelper)

		id, err := startRegistryContainer(ctx, dockerHelper)
		if err == nil {
			return id, nil
		}
		lastErr = err
		if attempt < registryStartAttempts {
			ginkgo.GinkgoWriter.Printf(
				"[retry] start local registry: attempt %d failed, retrying: %v\n", attempt, lastErr,
			)
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				return "", fmt.Errorf("start local registry: %w", ctx.Err())
			}
		}
	}
	return "", fmt.Errorf("after %d attempts: %w", registryStartAttempts, lastErr)
}
