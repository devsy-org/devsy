// Package serverutil holds the install and launch helpers shared by the
// browser IDE servers under pkg/ide.
package serverutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	copy2 "github.com/devsy-org/devsy/pkg/copy"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/util"
)

// SuOrSh builds an *exec.Cmd that runs runCommand either as the unprivileged
// user (via `su <user> -c`) or as the current user (via `sh -c`). All arg
// elements are constants from this package - runCommand itself is built from
// constants plus internal values, not user-controlled binary paths.
func SuOrSh(userName, runCommand, workingDir string) *exec.Cmd {
	var cmd *exec.Cmd
	if userName != "" {
		// #nosec G204 -- args are fixed constants; runCommand is internally built.
		cmd = exec.Command("su", userName, "-c", runCommand)
	} else {
		// #nosec G204 -- args are fixed constants; runCommand is internally built.
		cmd = exec.Command("sh", "-c", runCommand)
	}
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	return cmd
}

// UserHome returns the home directory of userName, or of the current user when
// userName is empty.
func UserHome(userName string) (string, error) {
	if userName != "" {
		return command.GetHome(userName)
	}
	return util.UserHomeDir()
}

// PrepareLocation returns <home>/<dirName> for userName, creating it if needed.
func PrepareLocation(userName, dirName string) (string, error) {
	homeFolder, err := UserHome(userName)
	if err != nil {
		return "", err
	}
	folder := filepath.Join(homeFolder, dirName)
	// #nosec G301 -- match openvscode-server convention for parity.
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", err
	}
	return folder, nil
}

// ChownIfNeeded recursively hands location to userName. It is a no-op when
// userName is empty.
func ChownIfNeeded(userName, location string) error {
	if userName == "" {
		return nil
	}
	if err := copy2.ChownR(location, userName); err != nil {
		return fmt.Errorf("chown: %w", err)
	}
	return nil
}

// ReleaseSpec describes how one IDE picks its server download URL per arch.
type ReleaseSpec struct {
	Options       ide.Options
	Amd64Option   string
	Arm64Option   string
	Amd64Template string
	Arm64Template string
}

// URL returns the value of the arch's override option when set, otherwise the
// arch's template filled with args. Any goarch other than "arm64" selects the
// amd64 settings.
func (s ReleaseSpec) URL(values map[string]config.OptionValue, goarch string, args ...any) string {
	if goarch == "arm64" {
		if url := s.Options.GetValue(values, s.Arm64Option); url != "" {
			return url
		}
		return fmt.Sprintf(s.Arm64Template, args...)
	}
	if url := s.Options.GetValue(values, s.Amd64Option); url != "" {
		return url
	}
	return fmt.Sprintf(s.Amd64Template, args...)
}

// ExtensionInstall runs one CLI install per extension, logging each outcome.
type ExtensionInstall struct {
	UserName   string
	Extensions []string
	// Command returns the shell command that installs one extension.
	Command func(extension string) string
	// Quote formats an extension name for log lines. Nil logs names as-is.
	Quote func(string) string
	// FailOnTotalFailure makes Run return an error when every extension fails.
	// Partial failures are always logged and tolerated.
	FailOnTotalFailure bool
}

// Run installs each extension in order. Failures are logged and collected.
func (e ExtensionInstall) Run() error {
	if len(e.Extensions) == 0 {
		return nil
	}
	quote := e.Quote
	if quote == nil {
		quote = func(name string) string { return name }
	}

	out := log.Writer(log.LevelInfo)
	defer func() { _ = out.Close() }()

	var failed []string
	for _, extension := range e.Extensions {
		name := quote(extension)
		log.Info("Install extension " + name)
		cmd := SuOrSh(e.UserName, e.Command(extension), "")
		cmd.Stdout = out
		cmd.Stderr = out
		if err := cmd.Run(); err != nil {
			log.Errorf("failed installing extension: extension=%s, error=%v", name, err)
			failed = append(failed, extension)
		} else {
			log.Infof("installed extension: extension=%s", name)
		}
	}

	if e.FailOnTotalFailure && len(failed) == len(e.Extensions) {
		return fmt.Errorf("all %d extensions failed to install: %v", len(failed), failed)
	}
	return nil
}
