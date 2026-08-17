package docker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func withoutLinger(t *testing.T) {
	t.Helper()
	original := lingerDir
	lingerDir = t.TempDir()
	t.Cleanup(func() { lingerDir = original })
}

func TestLingerWarning_ProbeFailureDoesNotWarn(t *testing.T) {
	withoutLinger(t)
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "podman-fake", "#!/bin/sh\n"+
		"case \"$1\" in\n"+
		"  --version) echo \"podman version 4.0.0\" ;;\n"+
		"  *) echo oops >&2; exit 1 ;;\n"+
		"esac\n")

	h := &DockerHelper{DockerCommand: bin}
	assert.Empty(t, h.LingerWarning(context.Background()),
		"a failed rootless probe must not be treated as a confirmed rootless daemon")
}

func TestLingerWarning_RootfulDoesNotWarn(t *testing.T) {
	withoutLinger(t)
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "podman-fake", "#!/bin/sh\n"+
		"case \"$1\" in\n"+
		"  --version) echo \"podman version 4.0.0\" ;;\n"+
		"  *) echo false ;;\n"+
		"esac\n")

	h := &DockerHelper{DockerCommand: bin}
	assert.Empty(t, h.LingerWarning(context.Background()))
}

func TestLingerWarning_RootlessWithoutLingerWarns(t *testing.T) {
	withoutLinger(t)
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "podman-fake", "#!/bin/sh\n"+
		"case \"$1\" in\n"+
		"  --version) echo \"podman version 4.0.0\" ;;\n"+
		"  *) echo true ;;\n"+
		"esac\n")

	h := &DockerHelper{DockerCommand: bin}
	assert.NotEmpty(t, h.LingerWarning(context.Background()))
}

func TestLingerWarning_NonPodmanDoesNotWarn(t *testing.T) {
	withoutLinger(t)
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", "#!/bin/sh\n"+
		"case \"$1\" in\n"+
		"  --version) echo \"Docker version 27.0.0\" ;;\n"+
		"  *) echo true ;;\n"+
		"esac\n")

	h := &DockerHelper{DockerCommand: bin}
	assert.Empty(t, h.LingerWarning(context.Background()))
}
