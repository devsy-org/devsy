package git

import (
	"fmt"

	"github.com/spf13/pflag"
)

// CloneStrategy selects how much history and object data a clone fetches.
type CloneStrategy string

const (
	FullCloneStrategy     CloneStrategy = ""
	BloblessCloneStrategy CloneStrategy = "blobless"
	TreelessCloneStrategy CloneStrategy = "treeless"
	ShallowCloneStrategy  CloneStrategy = "shallow"
	BareCloneStrategy     CloneStrategy = "bare"
)

// strategyArgs is the single source of truth mapping each strategy to its
// `git clone` flags. It drives both argument construction and flag validation.
var strategyArgs = map[CloneStrategy][]string{
	FullCloneStrategy:     nil,
	BloblessCloneStrategy: {"--filter=blob:none"},
	TreelessCloneStrategy: {"--filter=tree:0"},
	ShallowCloneStrategy:  {flagDepth1},
	BareCloneStrategy:     {"--bare", flagDepth1},
}

// Option configures a clone performed by Repo.Clone or Repo.CloneFromInfo.
type Option func(*cloneConfig)

// WithCloneStrategy selects the clone strategy. An empty strategy is treated as
// FullCloneStrategy.
func WithCloneStrategy(strategy CloneStrategy) Option {
	return func(c *cloneConfig) { c.strategy = strategy }
}

// WithBranch clones only the named branch.
func WithBranch(branch string) Option {
	return func(c *cloneConfig) { c.branch = branch }
}

// WithCredentialHelper configures a git credential helper for the clone.
func WithCredentialHelper(helper string) Option {
	return func(c *cloneConfig) { c.credentialHelper = helper }
}

// WithRecursiveSubmodules clones submodules recursively.
func WithRecursiveSubmodules() Option {
	return func(c *cloneConfig) { c.recurseSubmodules = true }
}

// LFSMode controls how Git LFS is handled after a clone.
type LFSMode int

const (
	// LFSFull registers the LFS filters and downloads LFS content. This matches
	// git's default clone behavior and is the zero value.
	LFSFull LFSMode = iota
	// LFSSetupOnly registers the LFS filters but does not download content,
	// leaving pointer stubs. Future checkouts/pulls will hydrate on demand.
	LFSSetupOnly
	// LFSSkip does nothing: no filters, no download.
	LFSSkip
)

const lfsModeSkip = "skip"

// lfsModeNames maps each LFSMode to its CLI string, and back via Set.
var lfsModeNames = map[LFSMode]string{
	LFSFull:      "full",
	LFSSetupOnly: "setup-only",
	LFSSkip:      lfsModeSkip,
}

// LFSMode implements pflag.Value so it can back a CLI flag.
var _ pflag.Value = (*LFSMode)(nil)

func (m *LFSMode) Set(v string) error {
	for mode, name := range lfsModeNames {
		if name == v {
			*m = mode
			return nil
		}
	}
	return fmt.Errorf("unsupported git-lfs mode %q (want full, setup-only, or skip)", v)
}

func (m *LFSMode) Type() string {
	return "lfsMode"
}

func (m LFSMode) String() string {
	return lfsModeNames[m]
}

// WithLFSMode selects how Git LFS is handled after the clone. The default is
// LFSFull.
func WithLFSMode(mode LFSMode) Option {
	return func(c *cloneConfig) { c.lfsMode = mode }
}

// WithSkipLFS disables Git LFS smudge and hydration for the clone. Equivalent
// to WithLFSMode(LFSSkip).
func WithSkipLFS() Option {
	return WithLFSMode(LFSSkip)
}

// cloneConfig is the resolved set of clone options.
type cloneConfig struct {
	strategy          CloneStrategy
	branch            string
	credentialHelper  string
	recurseSubmodules bool
	lfsMode           LFSMode
}

func newCloneConfig(options ...Option) cloneConfig {
	var c cloneConfig
	for _, opt := range options {
		opt(&c)
	}
	return c
}

// args returns the `git clone` arguments for the config and given repository and target directory.
func (c cloneConfig) args(repository, targetDir string) []string {
	args := append([]string{subClone}, strategyArgs[c.strategy]...)
	if c.branch != "" {
		args = append(args, flagBranch, c.branch)
	}
	if c.credentialHelper != "" {
		args = append(args, flagConfig, "credential.helper="+c.credentialHelper)
	}
	if c.recurseSubmodules {
		args = append(args, "--recurse-submodules")
	}
	return append(args, repository, targetDir, flagProgress)
}

// CloneStrategy implements pflag.Value for CloneStrategy.
var _ pflag.Value = (*CloneStrategy)(nil)

func (s *CloneStrategy) Set(v string) error {
	if _, ok := strategyArgs[CloneStrategy(v)]; !ok {
		return fmt.Errorf("unsupported clone strategy %q", v)
	}
	*s = CloneStrategy(v)
	return nil
}

func (s *CloneStrategy) Type() string {
	return "cloneStrategy"
}

func (s *CloneStrategy) String() string {
	return string(*s)
}
