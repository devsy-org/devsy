//go:build !windows

package dependencies

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLookupUnixResolutionPreservesTraversal(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "other", "child"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "other", "docker"), nil, 0o600))
	for name, target := range map[string]string{
		"relative": "other/child", "absolute": filepath.Join(root, "other", "child"),
		"chain": "relative", "broken": "absent", "loop": "loop",
	} {
		require.NoError(t, os.Symlink(target, filepath.Join(root, name)))
	}
	for _, suffix := range []string{
		"/relative/../docker", "/absolute/../docker", "/chain/../docker",
		"/other/./child/..//docker", "/other/docker/", "/other/docker/..",
		"/broken", "/loop", "/relative/../../..", "/absolute/.",
	} {
		t.Run(suffix, func(t *testing.T) {
			path := root + suffix
			want, wantErr := filepath.EvalSymlinks(path)
			got, err := resolveLookupPath(context.Background(), path)
			if wantErr != nil {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

func TestLookupUnixCanceledBeforeMetadata(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer cancel()
	observed, err := inspectLookupFile(ctx, filepath.Join(t.TempDir(), "missing"))
	require.Equal(t, context.DeadlineExceeded, err)
	require.Empty(t, observed)
}
