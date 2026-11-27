package client

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnderDefaultCacheRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cache")
	previous := defaultCacheRoot
	defaultCacheRoot = root
	t.Cleanup(func() { defaultCacheRoot = previous })
	sep := string(filepath.Separator)
	cases := map[string]struct {
		path  string
		below bool
	}{
		"default":        {filepath.Join(root, "platform", "config.json"), true},
		"custom below":   {filepath.Join(root, "other", "config.json"), true},
		"direct child":   {filepath.Join(root, "config.json"), true},
		"extra sep":      {root + sep + sep + "platform" + sep + sep + "config.json", true},
		"dot component":  {root + sep + "." + sep + "platform" + sep + "config.json", true},
		"dotdot inside":  {root + sep + "a" + sep + ".." + sep + "platform" + sep + "c.json", true},
		"dotdot escapes": {root + sep + ".." + sep + "x" + sep + "config.json", false},
		"sibling prefix": {root + "-custom" + sep + "config.json", false},
		"parent":         {filepath.Dir(root), false},
		"elsewhere":      {filepath.Join(t.TempDir(), "config.json"), false},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.below, underDefaultCacheRoot(test.path))
		})
	}
	defaultCacheRoot = ""
	require.False(t, underDefaultCacheRoot(filepath.Join(root, "platform", "config.json")))
}
