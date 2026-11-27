package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckImage(t *testing.T) {
	for _, image := range []string{
		"busybox", "busybox:1.37", "ghcr.io/devsy-org/base:v1",
		"registry.example:5000/a/b@sha256:0123456789abcdef",
	} {
		require.NoError(t, CheckImage(image), image)
	}
	for _, image := range []string{
		"", "-", "-x", "--privileged", "--rm=true", "a b", " a", "a ", "a\tb", "a\nb",
		"a\rb", "a\x00b", "a\x7fb", "a\u00a0b", "a\u2028b", "a\x1bb",
	} {
		require.ErrorIs(t, CheckImage(image), ErrInvalidImage, "%q", image)
	}
}
