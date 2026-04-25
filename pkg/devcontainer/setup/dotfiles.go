package setup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/pkg/command"
	copy2 "github.com/devsy-org/devsy/pkg/copy"
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

	log.Infof("Installing dotfiles from %s (user=%s)", cfg.Repository, cfg.RemoteUser)

	targetDir, err := dotfilesTargetDir(cfg.RemoteUser)
	if err != nil {
		return err
	}

	if err := cloneDotfiles(ctx, cfg.Repository, targetDir); err != nil {
		return err
	}

	// Chown the cloned dotfiles to the remote user so they are accessible
	// when the user SSHes into the container.
	if cfg.RemoteUser != "" {
		if err := copy2.ChownR(targetDir, cfg.RemoteUser); err != nil {
			log.Warnf("chown dotfiles dir: %v", err)
		}
	}

	return installDotfiles(ctx, cfg, targetDir)
}

func dotfilesTargetDir(remoteUser string) (string, error) {
	home, err := command.GetHome(remoteUser)
	if err != nil {
		return "", fmt.Errorf("resolve home for user %q: %w", remoteUser, err)
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
	cfg DotfilesConfig,
	targetDir string,
) error {
	if err := os.Chdir(targetDir); err != nil {
		return fmt.Errorf("enter dotfiles directory: %w", err)
	}

	if cfg.InstallScript != "" {
		return runDotfilesScript(ctx, cfg.InstallScript, cfg.RemoteUser)
	}

	return runKnownDotfilesScripts(ctx, cfg.RemoteUser)
}

func runDotfilesScript(ctx context.Context, script, remoteUser string) error {
	log.Infof("Executing dotfiles install script %s", script)
	p := "./" + strings.TrimPrefix(script, "./")

	if err := ensureDotfileExecutable(ctx, p); err != nil {
		return err
	}

	return execDotfilesCmd(ctx, p, remoteUser)
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

func runKnownDotfilesScripts(ctx context.Context, remoteUser string) error {
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
		if err := execDotfilesCmd(ctx, s, remoteUser); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		log.Debug("dotfiles install script executed")
		return nil
	}

	log.Info("No install script found, linking dotfiles")
	return linkAllDotfiles(remoteUser)
}

func linkAllDotfiles(remoteUser string) error {
	pwd, err := os.Getwd()
	if err != nil {
		return err
	}

	files, err := os.ReadDir(".")
	if err != nil {
		return err
	}

	home, err := command.GetHome(remoteUser)
	if err != nil {
		return fmt.Errorf("resolve home for user %q: %w", remoteUser, err)
	}
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

func execDotfilesCmd(ctx context.Context, script, remoteUser string) error {
	writer := log.Writer(log.LevelInfo)

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("get current user: %w", err)
	}

	var cmd *exec.Cmd
	if remoteUser != "" && remoteUser != currentUser.Username {
		// Run the install script as the remote user, matching the official
		// devcontainer CLI behaviour.
		// #nosec G204 -- user-provided dotfile install script path
		cmd = exec.CommandContext(ctx, "su", remoteUser, "-c", script)
	} else {
		cmd = exec.CommandContext(ctx, script)
	}
	cmd.Stdout = writer
	cmd.Stderr = writer
	return cmd.Run()
}
