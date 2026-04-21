package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/git"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// InstallDotfilesCmd holds the installDotfiles cmd flags.
type InstallDotfilesCmd struct {
	*flags.GlobalFlags

	Repository            string
	InstallScript         string
	StrictHostKeyChecking bool
}

// NewInstallDotfilesCmd creates a new command.
func NewInstallDotfilesCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &InstallDotfilesCmd{
		GlobalFlags: flags,
	}
	installDotfilesCmd := &cobra.Command{
		Use:   "install-dotfiles",
		Short: "installs input dotfiles in the container",
		Args:  cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}
	installDotfilesCmd.Flags().
		StringVar(&cmd.Repository, "repository", "", "The dotfiles repository")
	installDotfilesCmd.Flags().
		StringVar(&cmd.InstallScript, "install-script", "", "The dotfiles install command to execute")
	installDotfilesCmd.Flags().
		BoolVar(&cmd.StrictHostKeyChecking, "strict-host-key-checking", false,
			"Set to enable strict host key checking for git cloning via SSH")
	return installDotfilesCmd
}

// Run runs the command logic.
func (cmd *InstallDotfilesCmd) Run(ctx context.Context) error {
	targetDir := filepath.Join(os.Getenv("HOME"), "dotfiles")

	_, err := os.Stat(targetDir)
	if err != nil {
		log.Infof("Cloning dotfiles %s", cmd.Repository)

		gitInfo := git.NormalizeRepositoryGitInfo(cmd.Repository)
		if err := git.CloneRepository(
			ctx,
			gitInfo,
			targetDir,
			"",
			cmd.StrictHostKeyChecking,
		); err != nil {
			return err
		}
	} else {
		log.Info("dotfiles already set up, skipping cloning")
	}

	log.Debugf("Entering dotfiles directory")

	err = os.Chdir(targetDir)
	if err != nil {
		return err
	}

	if cmd.InstallScript != "" {
		log.Infof("Executing install script %s", cmd.InstallScript)
		command := "./" + strings.TrimPrefix(cmd.InstallScript, "./")

		err := ensureExecutable(ctx, command)
		if err != nil {
			return fmt.Errorf("failed to make install script %s executable: %w", command, err)
		}

		scriptCmd := exec.CommandContext(
			ctx,
			command,
		) // #nosec G204 -- user-provided dotfile install script
		writer := log.Writer(log.LevelInfo)
		scriptCmd.Stdout = writer
		scriptCmd.Stderr = writer

		return scriptCmd.Run()
	}

	log.Debugf("Install script not specified, trying known locations")

	return setupDotfiles(ctx)
}

var installScriptPaths = []string{
	"./install.sh",
	"./install",
	"./bootstrap.sh",
	"./bootstrap",
	"./script/bootstrap",
	"./setup.sh",
	"./setup",
	"./setup/setup",
}

func setupDotfiles(ctx context.Context) error {
	scripts := slices.Clone(installScriptPaths)
	scripts = slices.DeleteFunc(scripts, func(path string) bool {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return true
		}

		return false
	})

	for _, installScriptPath := range scripts {
		writer := log.Writer(log.LevelInfo)
		err := ensureExecutable(ctx, installScriptPath)
		if err != nil {
			log.Debugf(
				"install script not found: scriptPath=%s, error=%v",
				installScriptPath,
				err,
			)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}

		log.Debugf("executing dotfile install script: scriptPath=%s", installScriptPath)
		scriptCmd := exec.CommandContext(
			ctx,
			installScriptPath,
		) // #nosec G204 -- user-provided dotfile install script
		scriptCmd.Stdout = writer
		scriptCmd.Stderr = writer
		if err := scriptCmd.Run(); err != nil {
			log.Debugf("script execution failed: error=%v", err)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}

		// exit after first successful script
		log.Debug("install script executed")
		return nil
	}

	log.Info("Finished script locations, trying to link the files")

	return linkDotfiles(ctx)
}

func linkDotfiles(ctx context.Context) error {
	files, err := os.ReadDir(".")
	if err != nil {
		return err
	}

	pwd, err := os.Getwd()
	if err != nil {
		return err
	}

	home := os.Getenv("HOME")
	for _, file := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !strings.HasPrefix(file.Name(), ".") || file.IsDir() {
			continue
		}
		if err := linkDotfile(
			filepath.Join(pwd, file.Name()),
			filepath.Join(home, file.Name()),
		); err != nil {
			return err
		}
	}

	return nil
}

func linkDotfile(src, dest string) error {
	log.Debugf("linking %s in home", filepath.Base(dest))
	if _, err := os.Lstat(dest); err == nil { // #nosec G703
		if removeErr := os.Remove(dest); removeErr != nil { // #nosec G703
			log.Debugf("failed to remove %s: %v", dest, removeErr)
		}
	}
	return os.Symlink(src, dest)
}

func ensureExecutable(ctx context.Context, path string) error {
	checkCmd := exec.CommandContext(
		ctx,
		"test",
		"-f",
		path,
	) // #nosec G204 -- user-provided dotfile path
	err := checkCmd.Run()
	if err != nil {
		return fmt.Errorf("install script %s not found: %w", path, err)
	}

	chmodCmd := exec.CommandContext(
		ctx,
		"chmod",
		"+x",
		path,
	) // #nosec G204 -- user-provided dotfile path
	err = chmodCmd.Run()
	if err != nil {
		return fmt.Errorf("failed to make install script %s executable: %w", path, err)
	}

	return nil
}
