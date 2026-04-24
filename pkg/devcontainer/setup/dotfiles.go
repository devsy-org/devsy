package setup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/pkg/git"
	"github.com/devsy-org/devsy/pkg/log"
)

// PhaseDotfiles is a synthetic lifecycle phase for dotfiles installation.
// Per the devcontainer spec, dotfiles run after postCreateCommand and
// before postStartCommand.
const PhaseDotfiles LifecyclePhase = "dotfilesInstall"

// RunDotfiles clones the dotfiles repository and runs the install script
// inside the container. It is a no-op when repo is empty. The caller is
// responsible for marker-file checks.
func RunDotfiles(ctx context.Context, cfg DotfilesConfig) error {
	if cfg.Repository == "" {
		return nil
	}

	log.Infof("Installing dotfiles from %s", cfg.Repository)

	targetDir, err := dotfilesTargetDir()
	if err != nil {
		return err
	}

	if err := cloneDotfiles(ctx, cfg.Repository, targetDir); err != nil {
		return err
	}

	return installDotfiles(ctx, cfg.InstallScript, targetDir)
}

func dotfilesTargetDir() (string, error) {
	home := os.Getenv("HOME")
	if home == "" {
		return "", fmt.Errorf("HOME environment variable not set")
	}
	return filepath.Join(home, "dotfiles"), nil
}

func cloneDotfiles(ctx context.Context, repo, targetDir string) error {
	if _, err := os.Stat(targetDir); err == nil {
		log.Info("dotfiles already cloned, skipping")
		return nil
	}

	log.Infof("Cloning dotfiles %s", repo)
	gitInfo := git.NormalizeRepositoryGitInfo(repo)
	return git.CloneRepository(ctx, gitInfo, targetDir, "", false)
}

func installDotfiles(
	ctx context.Context,
	script, targetDir string,
) error {
	if err := os.Chdir(targetDir); err != nil {
		return fmt.Errorf("enter dotfiles directory: %w", err)
	}

	if script != "" {
		return runDotfilesScript(ctx, script)
	}

	return runKnownDotfilesScripts(ctx)
}

func runDotfilesScript(ctx context.Context, script string) error {
	log.Infof("Executing dotfiles install script %s", script)
	p := "./" + strings.TrimPrefix(script, "./")

	if err := ensureDotfileExecutable(ctx, p); err != nil {
		return err
	}

	return execDotfilesCmd(ctx, p)
}

var knownInstallScripts = []string{
	"./install.sh",
	"./install",
	"./bootstrap.sh",
	"./bootstrap",
	"./script/bootstrap",
	"./setup.sh",
	"./setup",
	"./setup/setup",
}

func runKnownDotfilesScripts(ctx context.Context) error {
	scripts := slices.DeleteFunc(
		slices.Clone(knownInstallScripts),
		func(p string) bool {
			_, err := os.Stat(p)
			return os.IsNotExist(err)
		},
	)

	for _, s := range scripts {
		if err := ensureDotfileExecutable(ctx, s); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		if err := execDotfilesCmd(ctx, s); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		log.Debug("dotfiles install script executed")
		return nil
	}

	log.Info("No install script found, linking dotfiles")
	return linkAllDotfiles()
}

func linkAllDotfiles() error {
	pwd, err := os.Getwd()
	if err != nil {
		return err
	}

	files, err := os.ReadDir(".")
	if err != nil {
		return err
	}

	home := os.Getenv("HOME")
	for _, f := range files {
		if !strings.HasPrefix(f.Name(), ".") || f.IsDir() {
			continue
		}
		src := filepath.Join(pwd, f.Name())
		dest := filepath.Join(home, f.Name())
		if err := linkDotfile(src, dest); err != nil {
			return err
		}
	}
	return nil
}

func linkDotfile(src, dest string) error {
	log.Debugf("linking %s in home", filepath.Base(dest))
	cleanDest := filepath.Clean(dest)
	if _, err := os.Lstat(cleanDest); err == nil {
		_ = os.Remove(cleanDest) // #nosec G703 -- user dotfile path
	}
	return os.Symlink(src, cleanDest)
}

func ensureDotfileExecutable(ctx context.Context, path string) error {
	// #nosec G204 -- user-provided dotfile install script path
	checkCmd := exec.CommandContext(ctx, "test", "-f", path)
	if err := checkCmd.Run(); err != nil {
		return fmt.Errorf("install script %s not found: %w", path, err)
	}

	// #nosec G204 -- user-provided dotfile install script path
	chmodCmd := exec.CommandContext(ctx, "chmod", "+x", path)
	if err := chmodCmd.Run(); err != nil {
		return fmt.Errorf("chmod +x %s: %w", path, err)
	}
	return nil
}

func execDotfilesCmd(ctx context.Context, script string) error {
	writer := log.Writer(log.LevelInfo)
	cmd := exec.CommandContext(ctx, script)
	cmd.Stdout = writer
	cmd.Stderr = writer
	return cmd.Run()
}
