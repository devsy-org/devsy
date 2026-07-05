package git

import (
	"context"
	"errors"
	"fmt"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/log"
)

// InstallBinary installs the git binary if it is not already available, using a
// system package manager.
func InstallBinary(ctx context.Context) error {
	return newInstaller(defaultRunner).ensure(ctx, gitTool)
}

// InstallLFS installs the git-lfs binary if it is not already available.
func InstallLFS(ctx context.Context) error {
	return newInstaller(defaultRunner).ensure(ctx, lfsTool)
}

// tool identifies a git-related binary the installer can provide.
type tool struct {
	binary  string
	pkg     string
	release *releaseSource
}

var (
	gitTool = tool{binary: binGit, pkg: binGit}
	lfsTool = tool{binary: binGitLFS, pkg: binGitLFS, release: &gitLFSRelease}
)

// installStrategy installs a package.
type installStrategy interface {
	name() string
	usable() bool
	install(ctx context.Context, t tool) error
}

// Installer installs git-related tools using an ordered list of strategies.
type Installer struct {
	strategies []installStrategy
}

// newInstaller builds an Installer with the default strategies.
func newInstaller(runner Runner) *Installer {
	return &Installer{
		strategies: []installStrategy{
			&pkgManagerStrategy{
				manager: strategyApt, runner: runner,
				installArgs: func(pkg string) [][]string {
					return [][]string{{pkgUpdate}, {"-y", lfsInstall, pkg}}
				},
			},
			&pkgManagerStrategy{
				manager: strategyApk, runner: runner,
				installArgs: func(pkg string) [][]string {
					return [][]string{{pkgUpdate}, {"add", pkg}}
				},
			},
			&releaseStrategy{},
		},
	}
}

// ensure installs the tool if it is not already available.
func (i *Installer) ensure(ctx context.Context, t tool) error {
	if command.Exists(t.binary) {
		return nil
	}

	tried := []string{}
	var errs []error
	for _, s := range i.strategies {
		if !s.usable() {
			continue
		}
		tried = append(tried, s.name())
		if err := s.install(ctx, t); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.name(), err))
			continue
		}
		if command.Exists(t.binary) {
			log.Infof("installed %s via %s", t.binary, s.name())
			return nil
		}
		errs = append(
			errs,
			fmt.Errorf("%s: reported success but %s is not on PATH", s.name(), t.binary),
		)
	}

	if len(tried) == 0 {
		return fmt.Errorf("no usable strategy to install %s", t.binary)
	}
	return fmt.Errorf("install %s (tried: %v): %w", t.binary, tried, errors.Join(errs...))
}

type pkgManagerStrategy struct {
	manager     string
	runner      Runner
	installArgs func(pkg string) [][]string
}

func (s *pkgManagerStrategy) name() string { return s.manager }

func (s *pkgManagerStrategy) usable() bool { return command.Exists(s.manager) }

func (s *pkgManagerStrategy) install(ctx context.Context, t tool) error {
	log.Infof("installing %s with %s", t.pkg, s.manager)
	w := log.Writer(log.LevelInfo)
	defer func() { _ = w.Close() }()

	for _, args := range s.installArgs(t.pkg) {
		if _, err := s.runner.Run(ctx, RunOptions{
			Binary: s.manager,
			Args:   args,
			Stdout: w,
			Stderr: w,
		}); err != nil {
			return err
		}
	}
	return nil
}

// releaseStrategy installs a tool by downloading its GitHub release asset.
type releaseStrategy struct{}

func (releaseStrategy) name() string { return strategyRelease }

func (releaseStrategy) usable() bool { return true }

func (releaseStrategy) install(ctx context.Context, t tool) error {
	if t.release == nil {
		return fmt.Errorf("%s has no release download available", t.binary)
	}
	return t.release.install(ctx, t.binary)
}
