package snapshot

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/google/go-containerregistry/pkg/name"
)

// dockerInternalHost is the well-known hostname Docker Desktop/OrbStack/etc.
// resolve, from inside any container, to the host machine itself — used by
// local registry fixtures (e.g. e2e tests) that must be reachable both from
// the host CLI process and from a workspace's own container, where
// "localhost" would otherwise resolve to the container's own loopback.
//
// Unlike "localhost"/"127.0.0.1" (which name.ParseReference already treats
// as insecure by IP/address, not by a lookup-able name), this is a plain
// hostname: DNS or /etc/hosts in some environment could legitimately point
// it at a real, non-local registry. So this override only takes effect when
// config.EnvInsecureDockerInternal is set, keeping it an explicit opt-in for
// local dev/test fixtures rather than an automatic judgment call baked into
// every snapshot registry operation.
const dockerInternalHost = "host.docker.internal"

// referenceOptions returns the name.Option set to use when parsing s as a
// snapshot registry reference, adding name.Insecure for registries that are
// always local to the calling machine (see config.EnvInsecureDockerInternal).
func referenceOptions(s string) []name.Option {
	if os.Getenv(config.EnvInsecureDockerInternal) != "" && isDockerInternalHost(s) {
		return []name.Option{name.Insecure}
	}
	return nil
}

// isDockerInternalHost reports whether s (a full reference, repository, or
// bare host[:port]) is rooted at dockerInternalHost.
func isDockerInternalHost(s string) bool {
	host := s
	if i := strings.IndexAny(host, "/@"); i >= 0 {
		host = host[:i]
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		// Guard against stripping a port-less IPv6-ish or scheme-less host:tag
		// pair incorrectly; dockerInternalHost never contains a colon itself.
		if host[:i] == dockerInternalHost {
			return true
		}
	}
	return host == dockerInternalHost
}

// parseReference, parseRepository, and parseTag wrap their name.* equivalents
// with referenceOptions.
func parseReference(s string) (name.Reference, error) {
	return name.ParseReference(s, referenceOptions(s)...)
}

func parseRepository(s string) (name.Repository, error) {
	return name.NewRepository(s, referenceOptions(s)...)
}

func parseTag(s string) (name.Tag, error) {
	return name.NewTag(s, referenceOptions(s)...)
}

// CheckPushPermissions checks push permission for imageRef, honoring this
// package's dockerInternalHost insecure-registry override.
func CheckPushPermissions(ctx context.Context, imageRef string) error {
	ref, err := parseReference(imageRef)
	if err != nil {
		return fmt.Errorf("parse image reference %q: %w", imageRef, err)
	}
	return image.CheckPushPermissionsRef(ctx, ref)
}

// ParseImageReference parses s as an image reference, honoring this
// package's dockerInternalHost insecure-registry override.
func ParseImageReference(s string) (name.Reference, error) {
	return parseReference(s)
}
