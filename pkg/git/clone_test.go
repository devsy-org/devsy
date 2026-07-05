package git

import (
	"testing"

	"gotest.tools/assert"
	"gotest.tools/assert/cmp"
)

const (
	credHelperStore = "credential.helper=store" // #nosec G101 -- not a credential, a git config flag
	testDir         = "dir"
	testBranch      = "dev"
	testRepo        = "repo"
)

type cloneArgsCase struct {
	name     string
	options  []Option
	expected []string
}

var cloneArgsCases = []cloneArgsCase{
	{"default full clone", nil, []string{subClone, testRepo, testDir, flagProgress}},
	{
		"blobless strategy",
		[]Option{WithCloneStrategy(BloblessCloneStrategy)},
		[]string{subClone, "--filter=blob:none", testRepo, testDir, flagProgress},
	},
	{
		"treeless strategy",
		[]Option{WithCloneStrategy(TreelessCloneStrategy)},
		[]string{subClone, "--filter=tree:0", testRepo, testDir, flagProgress},
	},
	{
		"shallow strategy",
		[]Option{WithCloneStrategy(ShallowCloneStrategy)},
		[]string{subClone, flagDepth1, testRepo, testDir, flagProgress},
	},
	{
		"bare strategy",
		[]Option{WithCloneStrategy(BareCloneStrategy)},
		[]string{subClone, "--bare", flagDepth1, testRepo, testDir, flagProgress},
	},
	{
		"branch",
		[]Option{WithBranch(testBranch)},
		[]string{subClone, flagBranch, testBranch, testRepo, testDir, flagProgress},
	},
	{
		"credential helper",
		[]Option{WithCredentialHelper("store")},
		[]string{subClone, flagConfig, credHelperStore, testRepo, testDir, flagProgress},
	},
	{
		"recurse submodules",
		[]Option{WithRecursiveSubmodules()},
		[]string{subClone, "--recurse-submodules", testRepo, testDir, flagProgress},
	},
	{
		"all options compose in flag order",
		[]Option{
			WithCloneStrategy(ShallowCloneStrategy),
			WithBranch(testBranch),
			WithCredentialHelper("store"),
			WithRecursiveSubmodules(),
		},
		[]string{
			subClone, flagDepth1, flagBranch, testBranch,
			flagConfig, credHelperStore, "--recurse-submodules",
			testRepo, testDir, flagProgress,
		},
	},
}

func TestCloneConfigArgs(t *testing.T) {
	for _, tc := range cloneArgsCases {
		t.Run(tc.name, func(t *testing.T) {
			c := newCloneConfig(tc.options...)
			assert.Check(t, cmp.DeepEqual(tc.expected, c.args(testRepo, testDir)))
		})
	}
}

func TestCloneStrategySetValidates(t *testing.T) {
	valid := []string{"", "blobless", "treeless", "shallow", "bare"}
	for _, v := range valid {
		var s CloneStrategy
		assert.NilError(t, s.Set(v))
		assert.Equal(t, v, s.String())
	}

	var s CloneStrategy
	assert.Assert(t, s.Set("nonsense") != nil)
}

func TestCloneStrategyType(t *testing.T) {
	var s CloneStrategy
	assert.Equal(t, "cloneStrategy", s.Type())
}

func TestLFSModeFlagValue(t *testing.T) {
	cases := map[string]LFSMode{
		"full":       LFSFull,
		"setup-only": LFSSetupOnly,
		"skip":       LFSSkip,
	}
	for in, want := range cases {
		var m LFSMode
		assert.NilError(t, m.Set(in))
		assert.Equal(t, want, m)
		assert.Equal(t, in, m.String())
	}

	// Zero value renders as the default ("full") for flag help.
	var zero LFSMode
	assert.Equal(t, "full", zero.String())
	assert.Equal(t, "lfsMode", zero.Type())

	var m LFSMode
	assert.Assert(t, m.Set("nonsense") != nil)
}
