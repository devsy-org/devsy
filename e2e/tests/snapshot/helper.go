package snapshot

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/docker"
)

// registryHostPort is a fixed host port for the local registry fixture,
// rather than an ephemeral one (`-p 0:5000`): the Docker daemon's
// "insecure-registries" allowlist (see below) must name an exact host:port,
// with no wildcard/range syntax, so the port has to be known ahead of time.
// Fixed is safe here because specs in this file run serially, never two
// registries at once.
const registryHostPort = "15500"

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

// startLocalRegistry runs a disposable `registry:2` container bound to
// registryHostPort, mirroring the manual `docker run` pattern already used
// for fixture containers in tests/up/provider_docker.go. Returns the
// "host:port" address to push/pull against and a cleanup func. Deletes are
// enabled so e2e specs can exercise DeleteManifest against a real registry.
func startLocalRegistry(
	ctx context.Context, dockerHelper *docker.DockerHelper,
) (string, func(), error) {
	// Best-effort: remove any registry container leaked by a previous
	// crashed/timed-out run, since it would otherwise permanently hold
	// registryHostPort and fail every subsequent run with a confusing
	// port-already-allocated error instead of the real cause.
	labels := []string{"devsy-e2e-snapshot-registry=true"}
	if leaked, ferr := dockerHelper.FindContainer(ctx, labels); ferr == nil {
		for _, id := range leaked {
			_ = dockerHelper.Stop(ctx, id)
			_ = dockerHelper.Remove(ctx, id)
		}
	}

	// Capture the container ID directly off `docker run -d`'s stdout rather
	// than looking it up afterwards by a shared static label: a leaked
	// registry container from a previous (e.g. failed) spec would otherwise
	// make a label-based lookup ambiguous or match the wrong container.
	var stdout bytes.Buffer
	err := dockerHelper.Run(ctx, []string{
		"run", "-d",
		"--label", "devsy-e2e-snapshot-registry=true",
		"-e", "REGISTRY_STORAGE_DELETE_ENABLED=true",
		"-p", registryHostPort + ":5000",
		"registry:2",
	}, docker.Streams{Stdout: &stdout})
	if err != nil {
		return "", nil, fmt.Errorf("start local registry: %w", err)
	}

	id := strings.TrimSpace(stdout.String())
	if id == "" {
		return "", nil, fmt.Errorf("start local registry: docker run produced no container id")
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
