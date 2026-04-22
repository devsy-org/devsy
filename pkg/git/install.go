package git

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/log"
)

func InstallBinary() error {
	writer := log.Writer(log.LevelInfo)
	errwriter := log.Writer(log.LevelError)
	defer func() { _ = writer.Close() }()
	defer func() { _ = errwriter.Close() }()

	// try to install git via apt / apk
	switch {
	case command.Exists("apt"):
		if err := installGitWithApt(writer, errwriter); err != nil {
			return err
		}
	case command.Exists("apk"):
		if err := installGitWithApk(writer, errwriter); err != nil {
			return err
		}
	default:
		// TODO: use golang git implementation
		return fmt.Errorf("couldn't find a package manager to install git")
	}

	// is git available now?
	if !command.Exists("git") {
		return fmt.Errorf("couldn't install git")
	}

	log.Infof("installed git")

	return nil
}

func installGitWithApt(writer, errwriter io.Writer) error {
	log.Infof("Git command is missing, try to install git with apt...")

	if err := runCmd(writer, errwriter, "apt", "update"); err != nil {
		return fmt.Errorf("run apt update: %w", err)
	}

	if err := runCmd(writer, errwriter, "apt", "-y", "install", "git"); err != nil {
		return fmt.Errorf("run apt install git -y: %w", err)
	}

	return nil
}

func installGitWithApk(writer, errwriter io.Writer) error {
	log.Infof("Git command is missing, try to install git with apk...")

	if err := runCmd(writer, errwriter, "apk", "update"); err != nil {
		return fmt.Errorf("run apk update: %w", err)
	}

	if err := runCmd(writer, errwriter, "apk", "add", "git"); err != nil {
		return fmt.Errorf("run apk add git: %w", err)
	}

	return nil
}

func runCmd(stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.Command(name, args...) // #nosec G204 -- args are internally constructed
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	return cmd.Run()
}
