package vscode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	devsyopen "github.com/devsy-org/devsy/pkg/open"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/subprocess"
)

const listExtensionsArg = "--list-extensions"

const containersExtension = "ms-vscode-remote.remote-containers"

const remoteFolderScheme = "vscode-remote"

type OpenParams struct {
	SSHHost           string
	Folder            string
	NewWindow         bool
	Flavor            Flavor
	TunnelMode        bool
	SSHConnectTimeout string
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

const (
	routeCLI = "cli"
	routeURI = "uri"
)

type commandRunner func(ctx context.Context, path string, args ...string) ([]byte, error)

// hostLauncher holds the host side effects of launching an editor so tests can
// replace them without starting a GUI application.
type hostLauncher struct {
	lookPath func(file string) (string, error)
	run      commandRunner
	openURI  func(uri string) error
	goos     string
}

func newHostLauncher() hostLauncher {
	return hostLauncher{
		lookPath: exec.LookPath,
		run:      runHostCLI,
		openURI:  devsyopen.Run,
		goos:     runtime.GOOS,
	}
}

func runHostCLI(ctx context.Context, path string, args ...string) ([]byte, error) {
	if !validHostCLI(path) || !validHostCLIArgs(args) {
		return nil, errors.New("unsupported editor command")
	}
	options := subprocess.Options{Redactor: secrets.NewEnvironmentRedactor(os.Environ())}
	var stdout bytes.Buffer
	if len(args) == 1 && args[0] == listExtensionsArg {
		// Extension enumeration is functional data, not a diagnostic tail.
		// Redacting or truncating it could hide an already-installed extension.
		options.Stdout = &stdout
	}
	_, err := subprocess.Run(ctx, path, args, options)
	return stdout.Bytes(), err
}

func validHostCLI(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	name := filepath.Base(path)
	switch strings.ToLower(filepath.Ext(name)) {
	case ".exe", ".cmd", ".bat", ".com":
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	for _, config := range openConfigs {
		if strings.EqualFold(name, config.cliName) {
			return true
		}
	}
	return false
}

func validHostCLIArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case listExtensionsArg:
		return len(args) == 1
	case "--install-extension":
		return validExtensionInstallArgs(args)
	default:
		return validFolderOpenArgs(args)
	}
}

func validExtensionInstallArgs(args []string) bool {
	if len(args) != 2 {
		return false
	}
	for _, config := range openConfigs {
		if args[1] == config.sshExtension {
			return true
		}
	}
	return false
}

func validFolderOpenArgs(args []string) bool {
	if len(args) >= 2 && args[0] == "--disable-extension" && args[1] == containersExtension {
		args = args[2:]
	}
	if len(args) != 3 {
		return false
	}
	switch args[0] {
	case "--new-window", "--reuse-window":
		return args[1] == "--folder-uri" && validRemoteFolderURI(args[2])
	default:
		return false
	}
}

func validRemoteFolderURI(rawURI string) bool {
	folder, err := url.Parse(rawURI)
	return err == nil &&
		folder.Scheme == remoteFolderScheme &&
		strings.HasPrefix(folder.Host, "ssh-remote+") &&
		strings.HasSuffix(folder.Host, pkgconfig.SSHHostSuffix)
}

// stageError reports which launch stage failed and a coarse class of the
// failure. Its text never includes the wrapped error or editor output: those
// routinely contain host paths, URLs and tokens that cannot be reliably
// redacted. The original error stays reachable through Unwrap.
type stageError struct {
	stage string
	class string
	err   error
}

func newStageError(stage string, err error) *stageError {
	return &stageError{stage: stage, class: classifyFailure(err), err: err}
}

func (e *stageError) Error() string { return e.stage + " failed: " + e.class }

func (e *stageError) Unwrap() error { return e.err }

func classifyFailure(err error) string {
	var exitErr *exec.ExitError
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &exitErr):
		return fmt.Sprintf("exit status %d", exitErr.ExitCode())
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		return "not found"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	default:
		return "error"
	}
}

func Open(ctx context.Context, params OpenParams) error {
	if params.SSHConnectTimeout == "" {
		EnsureHostSettings(params.Flavor)
	} else if err := ensureHostSettingsWithTimeout(params.Flavor, params.SSHConnectTimeout); err != nil {
		return fmt.Errorf("configure host Remote SSH timeout: %w", err)
	}

	return newHostLauncher().open(ctx, params)
}

// open dispatches a launch request to the editor. Success means the OS or the
// editor CLI accepted the request; it does not prove the editor stayed alive or
// that the remote SSH connection was established.
//
// Records carry the status operation ID from ctx so they can be matched to the
// up operation that requested the launch. They never include editor output or
// error text, only the stage, route and failure class.
func (l hostLauncher) open(ctx context.Context, params OpenParams) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := openConfigs[params.Flavor]; !ok {
		return newStageError(stageResolve, errors.New("unsupported editor flavor"))
	}
	redactor := secrets.NewEnvironmentRedactor(os.Environ())
	fields := []any{
		"operation", redactor.Redact(status.OperationID(ctx)),
		"flavor", redactor.Redact(string(params.Flavor)),
	}

	cliErr := l.openViaCLI(ctx, params, fields)
	if cliErr != nil {
		log.Warnw(
			"editor CLI launch failed",
			append(
				fields,
				"route",
				routeCLI,
				"stage",
				stageOf(cliErr),
				"class",
				classOf(cliErr),
			)...,
		)
	}
	if cliErr == nil {
		log.Infow("launch request dispatched", append(fields, "route", routeCLI)...)
		return nil
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	log.Debugw("dispatching launch request",
		append(fields, "route", routeURI, "stage", stageURIDispatch)...)
	uriErr := l.openViaURI(params)
	if uriErr == nil {
		log.Infow("launch request dispatched",
			append(fields, "route", routeURI, "after_cli_failure", true)...)
		return nil
	}

	log.Warnw("URI launch failed",
		append(fields, "route", routeURI, "stage", stageOf(uriErr), "class", classOf(uriErr))...)
	return errors.Join(cliErr, uriErr)
}

func stageOf(err error) string {
	if stageErr, ok := errors.AsType[*stageError](err); ok {
		return stageErr.stage
	}
	return ""
}

func classOf(err error) string {
	if stageErr, ok := errors.AsType[*stageError](err); ok {
		return stageErr.class
	}
	return classifyFailure(err)
}

func (l hostLauncher) openViaURI(params OpenParams) error {
	config, ok := openConfigs[params.Flavor]
	if !ok {
		return fmt.Errorf("unknown flavor %s", params.Flavor)
	}

	folder := strings.TrimPrefix(params.Folder, "/")
	pathStr := "/ssh-remote+" + params.SSHHost + "/" + folder
	u := &url.URL{
		Scheme: config.scheme,
		Host:   remoteFolderScheme,
		Path:   pathStr,
	}
	if params.NewWindow {
		q := u.Query()
		q.Set("windowId", "_blank")
		u.RawQuery = q.Encode()
	}
	openURL := u.String()

	if err := l.openURI(openURL); err != nil {
		return newStageError(stageURIDispatch, err)
	}

	return nil
}

func (l hostLauncher) openViaCLI(ctx context.Context, params OpenParams, fields []any) error {
	config, ok := openConfigs[params.Flavor]
	if !ok {
		return fmt.Errorf("unknown flavor %s", params.Flavor)
	}

	cliPath, err := l.resolveCLI(config, fields)
	if err != nil {
		return err
	}

	log.Debugw("enumerating editor extensions",
		append(fields, "route", routeCLI, "stage", stageListExtensions)...)
	hasSSHExtension, hasContainersExtension, err := l.listInstalledExtensions(
		ctx,
		cliPath,
		config.sshExtension,
	)
	if err != nil {
		return err
	}

	if !hasSSHExtension {
		log.Debugw("installing editor SSH extension",
			append(fields, "route", routeCLI, "stage", stageInstallSSHExt)...)
		if err := l.ensureSSHExtension(ctx, cliPath, config.sshExtension); err != nil {
			return err
		}
	}

	args := buildOpenArgs(params.SSHHost, params.Folder, params.NewWindow, hasContainersExtension)
	log.Debugw("dispatching launch request",
		append(fields, "route", routeCLI, "stage", stageCLIDispatch)...)
	if _, err := l.run(ctx, cliPath, args...); err != nil {
		return newStageError(stageCLIDispatch, err)
	}

	return nil
}

// resolveCLI finds the editor CLI. It logs only the file kind, not the path: on
// Windows a .cmd wrapper and a native executable fail in different ways, while
// the path itself usually contains the user's name.
func (l hostLauncher) resolveCLI(config openConfig, fields []any) (string, error) {
	candidates := []string{config.cliName}
	if l.goos == "darwin" {
		candidates = append(candidates, config.macAppPath)
	}

	var lookupErrs []error
	for _, candidate := range candidates {
		path, err := l.lookPath(candidate)
		if err == nil {
			path, err = filepath.Abs(path)
			if err != nil {
				lookupErrs = append(lookupErrs, err)
				continue
			}
			log.Debugw(
				"editor CLI resolved",
				append(
					fields,
					"route",
					routeCLI,
					"stage",
					stageResolve,
					"cli_kind",
					cliKind(path),
				)...)
			return path, nil
		}
		lookupErrs = append(lookupErrs, err)
	}

	return "", newStageError(stageResolve, errors.Join(lookupErrs...))
}

// cliKind classifies the resolved file by extension only. It does not show how
// the file is executed, so a "batch wrapper" result is not evidence about
// native Windows .cmd launch behavior.
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
	out, err := l.run(ctx, cliPath, listExtensionsArg)
	if err != nil {
		return false, false, newStageError(stageListExtensions, err)
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
	if _, err := l.run(ctx, cliPath, "--install-extension", sshExtension); err != nil {
		return newStageError(stageInstallSSHExt, err)
	}
	return nil
}

func buildOpenArgs(sshHost, folder string, newWindow, hasContainersExtension bool) []string {
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
		Scheme: remoteFolderScheme,
		Host:   "ssh-remote+" + sshHost,
		Path:   strings.TrimPrefix(folder, "/"),
	}
	folderURI := u.String()
	args = append(args, "--folder-uri", folderURI)

	return args
}
