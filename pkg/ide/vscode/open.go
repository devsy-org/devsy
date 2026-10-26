package vscode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devsy-org/devsy/pkg/command"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	devsyopen "github.com/devsy-org/devsy/pkg/open"
)

const containersExtension = "ms-vscode-remote.remote-containers"

type OpenParams struct {
	Workspace  string
	Folder     string
	NewWindow  bool
	Flavor     Flavor
	TunnelMode bool
}

type openConfig struct {
	scheme       string
	cliName      string
	macAppPath   string
	sshExtension string
}

var openConfigs = map[Flavor]openConfig{
	FlavorStable: {
		scheme:       "vscode",
		cliName:      "code",
		macAppPath:   "/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code",
		sshExtension: "ms-vscode-remote.remote-ssh",
	},
	FlavorInsiders: {
		scheme:       "vscode-insiders",
		cliName:      "code-insiders",
		macAppPath:   "/Applications/Visual Studio Code - Insiders.app/Contents/Resources/app/bin/code",
		sshExtension: "ms-vscode-remote.remote-ssh",
	},
	FlavorCursor: {
		scheme:       "cursor",
		cliName:      "cursor",
		macAppPath:   "/Applications/Cursor.app/Contents/Resources/app/bin/cursor",
		sshExtension: "ms-vscode-remote.remote-ssh",
	},
	FlavorPositron: {
		scheme:       "positron",
		cliName:      "positron",
		macAppPath:   "/Applications/Positron.app/Contents/Resources/app/bin/positron",
		sshExtension: "ms-vscode-remote.remote-ssh",
	},
	FlavorCodium: {
		scheme:       "codium",
		cliName:      "codium",
		macAppPath:   "/Applications/Codium.app/Contents/Resources/app/bin/codium",
		sshExtension: "jeanp413.open-remote-ssh",
	},
	FlavorWindsurf: {
		scheme:       "windsurf",
		cliName:      "windsurf",
		macAppPath:   "/Applications/Windsurf.app/Contents/Resources/app/bin/windsurf",
		sshExtension: "ms-vscode-remote.remote-ssh",
	},
	FlavorAntigravity: {
		scheme:       "antigravity",
		cliName:      "agy",
		macAppPath:   "/Applications/Antigravity.app/Contents/Resources/app/bin/agy",
		sshExtension: "ms-vscode-remote.remote-ssh",
	},
	FlavorBob: {
		scheme:       "bob",
		cliName:      "bobide",
		macAppPath:   "/Applications/Bobide.app/Contents/Resources/app/bin/bobide",
		sshExtension: "jeanp413.open-remote-ssh",
	},
}

// Launch stages prefix errors so a failure can be traced to the step that
// produced it rather than to the overall launch.
const (
	stageResolve        = "resolve executable"
	stageListExtensions = "list extensions"
	stageInstallSSHExt  = "install SSH extension"
	stageCLIDispatch    = "CLI dispatch"
	stageURIDispatch    = "URI dispatch"
)

const maxLoggedOutputBytes = 2048

type commandRunner func(ctx context.Context, path string, args ...string) ([]byte, error)

// hostLauncher holds the host side effects of launching an editor so tests can
// replace them without starting a GUI application.
type hostLauncher struct {
	lookPath func(file string) (string, error)
	run      commandRunner
	openURI  func(uri string) error
	goos     string
	homeDir  string
}

func newHostLauncher() hostLauncher {
	homeDir, _ := os.UserHomeDir()
	return hostLauncher{
		lookPath: exec.LookPath,
		run: func(ctx context.Context, path string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, path, args...).CombinedOutput()
		},
		openURI: devsyopen.Run,
		goos:    runtime.GOOS,
		homeDir: homeDir,
	}
}

func Open(ctx context.Context, params OpenParams) error {
	EnsureHostSettings(params.Flavor)

	return newHostLauncher().open(ctx, params)
}

// open dispatches a launch request to the editor. Success means the OS or the
// editor CLI accepted the request; it does not prove the editor stayed alive or
// that the remote SSH connection was established.
func (l hostLauncher) open(ctx context.Context, params OpenParams) error {
	name := params.Flavor.DisplayName()

	cliErr := l.openViaCLI(ctx, params)
	if cliErr == nil {
		log.Infof("launch request dispatched to %s through its CLI", name)
		return nil
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	log.Warnf("%s CLI launch failed, falling back to URI handler: %v", name, cliErr)
	uriErr := l.openViaURI(params)
	if uriErr == nil {
		log.Infof(
			"launch request dispatched to %s through its URI handler after CLI failure",
			name,
		)
		return nil
	}

	return errors.Join(cliErr, uriErr)
}

func (l hostLauncher) openViaURI(params OpenParams) error {
	config, ok := openConfigs[params.Flavor]
	if !ok {
		return fmt.Errorf("unknown flavor %s", params.Flavor)
	}

	folder := strings.TrimPrefix(params.Folder, "/")
	pathStr := fmt.Sprintf("/ssh-remote+%s"+pkgconfig.SSHHostSuffix+"/%s", params.Workspace, folder)
	u := &url.URL{
		Scheme: config.scheme,
		Host:   "vscode-remote",
		Path:   pathStr,
	}
	if params.NewWindow {
		q := u.Query()
		q.Set("windowId", "_blank")
		u.RawQuery = q.Encode()
	}
	openURL := u.String()

	log.Debugf("opening URL %s", openURL)
	if err := l.openURI(openURL); err != nil {
		return fmt.Errorf("%s: flavor %s may not be installed on the host: %w",
			stageURIDispatch, params.Flavor.DisplayName(), err)
	}

	return nil
}

func (l hostLauncher) openViaCLI(ctx context.Context, params OpenParams) error {
	config, ok := openConfigs[params.Flavor]
	if !ok {
		return fmt.Errorf("unknown flavor %s", params.Flavor)
	}

	cliPath, err := l.resolveCLI(params.Flavor, config)
	if err != nil {
		return err
	}

	hasSSHExtension, hasContainersExtension, err := l.listInstalledExtensions(
		ctx,
		cliPath,
		config.sshExtension,
	)
	if err != nil {
		return err
	}

	if !hasSSHExtension {
		if err := l.ensureSSHExtension(ctx, cliPath, config.sshExtension); err != nil {
			return err
		}
	}

	args := buildOpenArgs(params.Workspace, params.Folder, params.NewWindow, hasContainersExtension)
	log.Debugf(
		"flavor %s command %s %s",
		params.Flavor.DisplayName(),
		cliPath,
		strings.Join(args, " "),
	)
	if _, err := l.runStage(ctx, stageCLIDispatch, cliPath, args...); err != nil {
		return err
	}

	return nil
}

// resolveCLI finds the editor CLI and records what kind of file it is: on
// Windows a .cmd wrapper and a native executable fail in different ways.
func (l hostLauncher) resolveCLI(flavor Flavor, config openConfig) (string, error) {
	candidates := []string{config.cliName}
	if l.goos == "darwin" {
		candidates = append(candidates, config.macAppPath)
	}

	for _, candidate := range candidates {
		path, err := l.lookPath(candidate)
		if err == nil {
			log.Debugf(
				"flavor %s CLI resolved to %s (%s)",
				flavor.DisplayName(),
				path,
				cliKind(path),
			)
			return path, nil
		}
	}

	return "", fmt.Errorf("%s: flavor %s binary is not found", stageResolve, flavor)
}

func cliKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		return "batch wrapper"
	case ".exe":
		return "native executable"
	case "":
		return "no extension"
	default:
		return "other"
	}
}

func (l hostLauncher) listInstalledExtensions(
	ctx context.Context,
	cliPath, sshExtension string,
) (hasSSH, hasContainers bool, err error) {
	out, err := l.runStage(ctx, stageListExtensions, cliPath, "--list-extensions")
	if err != nil {
		return false, false, err
	}

	for ext := range strings.SplitSeq(string(out), "\n") {
		ext = strings.TrimSpace(ext)
		switch ext {
		case sshExtension:
			hasSSH = true
		case containersExtension:
			hasContainers = true
		}

		if hasSSH && hasContainers {
			break
		}
	}

	return hasSSH, hasContainers, nil
}

func (l hostLauncher) ensureSSHExtension(ctx context.Context, cliPath, sshExtension string) error {
	args := []string{"--install-extension", sshExtension}
	log.Debugf("%s %s", cliPath, strings.Join(args, " "))
	_, err := l.runStage(ctx, stageInstallSSHExt, cliPath, args...)
	return err
}

// runStage runs the editor CLI and attaches the stage name and bounded output to
// any error, keeping the original error in the chain.
func (l hostLauncher) runStage(
	ctx context.Context,
	stage, cliPath string,
	args ...string,
) ([]byte, error) {
	out, err := l.run(ctx, cliPath, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", stage, command.WrapCommandError(l.boundOutput(out), err))
	}

	return out, nil
}

// boundOutput limits CLI output kept in errors and logs and masks the user's
// home directory, which commonly appears in editor error text.
func (l hostLauncher) boundOutput(out []byte) []byte {
	if l.homeDir != "" {
		out = bytes.ReplaceAll(out, []byte(l.homeDir), []byte("~"))
	}

	if len(out) > maxLoggedOutputBytes {
		out = append(out[:maxLoggedOutputBytes:maxLoggedOutputBytes], "... [truncated]"...)
	}

	return out
}

func buildOpenArgs(workspace, folder string, newWindow, hasContainersExtension bool) []string {
	args := make([]string, 0, 4)

	if hasContainersExtension {
		args = append(args, "--disable-extension", containersExtension)
	}

	if newWindow {
		args = append(args, "--new-window")
	} else {
		args = append(args, "--reuse-window")
	}

	u := &url.URL{
		Scheme: "vscode-remote",
		Host:   "ssh-remote+" + workspace + pkgconfig.SSHHostSuffix,
		Path:   strings.TrimPrefix(folder, "/"),
	}
	folderURI := u.String()
	args = append(args, "--folder-uri", folderURI)

	return args
}
