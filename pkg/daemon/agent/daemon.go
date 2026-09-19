package agent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/devsy-org/api/pkg/devsy"
	"github.com/devsy-org/devsy/pkg/command"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/machinediagnostics"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/google/shlex"
)

type SshConfig struct {
	Workdir string `json:"workdir,omitempty"`
	User    string `json:"user,omitempty"`
}

type DaemonConfig struct {
	Platform       devsy.PlatformOptions `json:"platform"`
	Ssh            SshConfig             `json:"ssh"`
	Timeout        string                `json:"timeout"`
	ShutdownAction string                `json:"shutdownAction,omitempty"`
}

func BuildWorkspaceDaemonConfig(
	platformOptions devsy.PlatformOptions,
	workspaceConfig *provider2.Workspace,
	substitutionContext *config.SubstitutionContext,
	mergedConfig *config.MergedDevContainerConfig,
) (*DaemonConfig, error) {
	var workdir string
	if workspaceConfig.Source.GitSubPath != "" {
		substitutionContext.ContainerWorkspaceFolder = filepath.Join(
			substitutionContext.ContainerWorkspaceFolder,
			workspaceConfig.Source.GitSubPath,
		)
		workdir = substitutionContext.ContainerWorkspaceFolder
	}
	if workdir == "" && mergedConfig != nil {
		workdir = mergedConfig.WorkspaceFolder
	}
	if workdir == "" && substitutionContext != nil {
		workdir = substitutionContext.ContainerWorkspaceFolder
	}

	// Get remote user; default to "root" if empty.
	user := mergedConfig.RemoteUser
	if user == "" {
		user = "root"
	}

	// build info isn't required in the workspace and can be omitted
	platformOptions.Build = nil

	shutdownAction := mergedConfig.ShutdownAction

	daemonConfig := &DaemonConfig{
		Platform: platformOptions,
		Ssh: SshConfig{
			Workdir: workdir,
			User:    user,
		},
		ShutdownAction: shutdownAction,
	}

	return daemonConfig, nil
}

func GetEncodedWorkspaceDaemonConfig(
	platformOptions devsy.PlatformOptions,
	workspaceConfig *provider2.Workspace,
	substitutionContext *config.SubstitutionContext,
	mergedConfig *config.MergedDevContainerConfig,
) (string, error) {
	daemonConfig, err := BuildWorkspaceDaemonConfig(
		platformOptions,
		workspaceConfig,
		substitutionContext,
		mergedConfig,
	)
	if err != nil {
		return "", err
	}

	data, err := json.Marshal(daemonConfig)
	if err != nil {
		return "", err
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	return encoded, nil
}

const systemdDir = "/etc/systemd/system"

func serviceName() string {
	return pkgconfig.BinaryName + ".service"
}

func serviceFilePath() string {
	return filepath.Join(systemdDir, serviceName())
}

func systemdUnitContents(execStart string) string {
	return fmt.Sprintf(`[Unit]
Description=%s
After=network.target

[Service]
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`, pkgconfig.DaemonServiceDescription, execStart)
}

// isSystemdAvailable returns true if systemd is the running init system.
// Checking /run/systemd/system is the systemd-recommended approach and
// correctly returns false inside containers or WSL where systemd is not PID 1.
func isSystemdAvailable() bool {
	fi, err := os.Stat("/run/systemd/system")
	if err != nil {
		return false
	}
	return fi.IsDir()
}

func isServiceInstalled() bool {
	_, err := os.Stat(serviceFilePath())
	return err == nil
}

func isServiceRunning() bool {
	//nolint:gosec // BinaryName is a compile-time constant, not tainted input
	out, err := exec.Command("systemctl", "is-active", pkgconfig.BinaryName).CombinedOutput()
	return err == nil && strings.TrimSpace(string(out)) == "active"
}

// quoteSystemdArg wraps an argument in double quotes if it contains characters
// that require quoting in systemd unit files. Literal percent signs are escaped
// as %% to prevent systemd specifier expansion.
func quoteSystemdArg(arg string) string {
	arg = strings.ReplaceAll(arg, "%", "%%")
	if strings.ContainsAny(arg, " \t\"\\") {
		arg = strings.ReplaceAll(arg, "\\", "\\\\")
		arg = strings.ReplaceAll(arg, "\"", "\\\"")
		return "\"" + arg + "\""
	}
	return arg
}

type InstallOptions struct {
	StateLocation     StateLocation
	Interval          string
	ShutdownAction    string
	DiagnosticsReader machinediagnostics.ReaderIdentity
}

func InstallDaemon(
	opts InstallOptions,
) error {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return fmt.Errorf("unsupported daemon os")
	}
	if err := opts.StateLocation.Validate(); err != nil {
		return fmt.Errorf("validate daemon state location: %w", err)
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	args := buildDaemonArgs(executable, opts)

	if !isSystemdAvailable() {
		log.Warnf("systemd not available, falling back to background process")
		lockPath, err := fallbackRuntimeLockPath(os.Geteuid(), os.UserCacheDir)
		if err != nil {
			return err
		}
		return startFallbackDaemon(executable, args, lockPath)
	}
	return installSystemdDaemon(opts, executable, args)
}

func installSystemdDaemon(opts InstallOptions, executable string, args []string) error {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = quoteSystemdArg(a)
	}
	unitContent := systemdUnitContents(strings.Join(quoted, " "))
	if err := rejectConflictingStateRoot(opts.StateLocation); err != nil {
		return err
	}

	needsReload, err := installOrUpdateUnit(unitContent)
	if err != nil {
		return err
	}

	return ensureServiceRunning(needsReload, executable, args)
}

// rejectConflictingStateRoot prevents a second machine lifecycle from silently
// replacing the singleton service's configured state root. Legacy units have
// no explicit root and remain upgradeable.
func rejectConflictingStateRoot(location StateLocation) error {
	if !isServiceInstalled() {
		return nil
	}
	existing, err := os.ReadFile(serviceFilePath())
	if err != nil {
		return fmt.Errorf("read existing daemon service: %w", err)
	}
	existingLocation, configured, err := daemonUnitStateLocation(string(existing))
	if err != nil {
		return fmt.Errorf("read existing daemon service state location: %w", err)
	}
	if !configured {
		return nil
	}
	if existingLocation == location {
		return nil
	}
	return fmt.Errorf(
		"devsy machine daemon is already configured for a different state root; " +
			"multiple machine state roots on one daemon are not supported",
	)
}

// daemonUnitStateLocation reads the generated daemon state flags from the
// ExecStart command. It compares parsed arguments, not source substrings, so
// roots such as /state/dev and /state/development remain distinct.
func daemonUnitStateLocation(unit string) (StateLocation, bool, error) {
	for line := range strings.SplitSeq(unit, "\n") {
		commandLine, found := strings.CutPrefix(line, "ExecStart=")
		if !found {
			continue
		}
		args, err := shlex.Split(commandLine)
		if err != nil {
			return StateLocation{}, false, fmt.Errorf("parse ExecStart: %w", err)
		}
		root, rootFound := commandFlagValue(args, names.Flag(names.StateRoot))
		if !rootFound {
			return StateLocation{}, false, nil
		}
		layout, layoutFound := commandFlagValue(args, names.Flag(names.StateLayout))
		if !layoutFound {
			return StateLocation{}, true, fmt.Errorf("%s is missing", names.Flag(names.StateLayout))
		}
		return StateLocation{Root: root, Layout: StateLayout(layout)}, true, nil
	}
	return StateLocation{}, false, nil
}

func commandFlagValue(args []string, flag string) (string, bool) {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == flag {
			return strings.ReplaceAll(args[index+1], "%%", "%"), true
		}
	}
	return "", false
}

func buildDaemonArgs(executable string, opts InstallOptions) []string {
	args := []string{
		executable,
		"internal",
		"agent",
		"daemon",
		names.Flag(names.StateRoot),
		opts.StateLocation.Root,
		names.Flag(names.StateLayout),
		string(opts.StateLocation.Layout),
		names.Flag(names.DiagnosticsReaderUID),
		strconv.Itoa(opts.DiagnosticsReader.UID),
		names.Flag(names.DiagnosticsReaderGID),
		strconv.Itoa(opts.DiagnosticsReader.GID),
	}
	if opts.Interval != "" {
		args = append(args, names.Flag(names.Interval), opts.Interval)
	}
	if opts.ShutdownAction != "" {
		args = append(args, names.Flag(names.ShutdownAction), opts.ShutdownAction)
	}
	return args
}

// DiagnosticsReaderIdentity captures the invoking user before systemd starts
// the daemon as root. SUDO_UID/GID identify the original user on sudo paths.
func DiagnosticsReaderIdentity() machinediagnostics.ReaderIdentity {
	uid, gid := os.Getuid(), os.Getgid()
	if uid == 0 {
		if parsedUID, err := strconv.Atoi(os.Getenv("SUDO_UID")); err == nil && parsedUID >= 0 {
			uid = parsedUID
		}
		if parsedGID, err := strconv.Atoi(os.Getenv("SUDO_GID")); err == nil && parsedGID >= 0 {
			gid = parsedGID
		}
	}
	return machinediagnostics.ReaderIdentity{UID: uid, GID: gid}
}

// installOrUpdateUnit writes the systemd unit file and runs daemon-reload when
// the service is not yet installed or its unit content has changed. It returns
// true when a reload was performed.
func installOrUpdateUnit(unitContent string) (bool, error) {
	needsReload := false
	if !isServiceInstalled() {
		needsReload = true
	} else {
		existing, err := os.ReadFile(serviceFilePath())
		if err != nil || string(existing) != unitContent {
			needsReload = true
		}
	}

	if !needsReload {
		return false, nil
	}

	//nolint:gosec // systemd unit files must be world-readable (0644)
	if err := os.WriteFile(
		serviceFilePath(), []byte(unitContent), 0o644,
	); err != nil {
		return false, fmt.Errorf("write service file: %w", err)
	}

	if out, err := exec.Command(
		"systemctl", "daemon-reload",
	).CombinedOutput(); err != nil {
		return false, fmt.Errorf("systemctl daemon-reload: %s: %w", string(out), err)
	}

	return true, nil
}

// ensureServiceRunning enables the systemd service and starts or restarts it as
// needed. If systemctl fails, it falls back to a background process.
func ensureServiceRunning(needsReload bool, executable string, args []string) error {
	// Always enable so the service starts on boot, even if it was previously disabled.
	//nolint:gosec // BinaryName is a compile-time constant, not tainted input
	if out, err := exec.Command("systemctl", "enable", pkgconfig.BinaryName).
		CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl enable: %s: %w", string(out), err)
	}

	// Restart if the unit file changed, otherwise just ensure it's running.
	if needsReload && isServiceRunning() {
		//nolint:gosec // BinaryName is a compile-time constant
		if out, err := exec.Command(
			"systemctl", "restart", pkgconfig.BinaryName,
		).CombinedOutput(); err != nil {
			log.Warnf("Error restarting service: %s: %v", string(out), err)
			return startFallbackDaemon(executable, args, machinediagnostics.DefaultRuntimeLockPath)
		}
		log.Infof("restarted Devsy daemon with updated config")
	} else if !isServiceRunning() {
		//nolint:gosec // BinaryName is a compile-time constant, not tainted input
		if out, err := exec.Command(
			"systemctl", "start", pkgconfig.BinaryName,
		).CombinedOutput(); err != nil {
			log.Warnf("Error starting service: %s: %v", string(out), err)
			return startFallbackDaemon(executable, args, machinediagnostics.DefaultRuntimeLockPath)
		}
		log.Infof("installed Devsy daemon into server")
	}

	return nil
}

func startFallbackDaemon(executable string, args []string, runtimeLockPath string) error {
	daemonArgs := args[1:] // strip executable path
	err := command.StartBackgroundOnce(pkgconfig.DaemonProcessName, func() (*exec.Cmd, error) {
		//nolint:gosec // executable is from os.Executable()
		cmd := exec.Command(executable, daemonArgs...)
		if runtimeLockPath != machinediagnostics.DefaultRuntimeLockPath {
			cmd.Env = append(os.Environ(), machinediagnostics.RuntimeLockPathEnv+"="+runtimeLockPath)
		}
		return cmd, nil
	})
	if err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	log.Infof("started Devsy daemon into server")
	return nil
}

func fallbackRuntimeLockPath(
	uid int,
	userCacheDir func() (string, error),
) (string, error) {
	if uid == 0 {
		return machinediagnostics.DefaultRuntimeLockPath, nil
	}
	cacheDir, err := userCacheDir()
	if err != nil {
		return "", fmt.Errorf("get user cache directory for daemon lock: %w", err)
	}
	return filepath.Join(cacheDir, "devsy", "agent-daemon.lock"), nil
}

func RemoveDaemon() error {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return fmt.Errorf("unsupported daemon os")
	}

	// Always attempt to stop the fallback background process, regardless of
	// systemd availability. InstallDaemon may have used the fallback path.
	if err := stopFallbackDaemon(); err != nil {
		return fmt.Errorf("stop fallback daemon: %w", err)
	}

	if !isServiceInstalled() {
		return nil
	}

	// stop and disable the service, propagating real errors
	if err := systemctlIgnoringNotLoaded("stop"); err != nil {
		return err
	}
	if err := systemctlIgnoringNotLoaded("disable"); err != nil {
		return err
	}

	return removeServiceUnit()
}

// systemctlIgnoringNotLoaded runs a systemctl action against the daemon service,
// treating a "not loaded" result (the unit does not exist) as a no-op.
func systemctlIgnoringNotLoaded(action string) error {
	//nolint:gosec // BinaryName is a compile-time constant
	out, err := exec.Command("systemctl", action, pkgconfig.BinaryName).CombinedOutput()
	// A missing unit reports "not loaded" (stop) or "does not exist" (disable);
	// both mean there is nothing to act on, so treat them as a no-op.
	outStr := string(out)
	if err != nil && !strings.Contains(outStr, "not loaded") &&
		!strings.Contains(outStr, "does not exist") {
		return fmt.Errorf("systemctl %s: %s: %w", action, outStr, err)
	}
	return nil
}

func removeServiceUnit() error {
	if err := os.Remove(serviceFilePath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove service file: %w", err)
	}

	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload: %s: %w", string(out), err)
	}

	return nil
}

// stopFallbackDaemon kills the PID-file-based background process started by
// command.StartBackgroundOnce and removes its PID file. It verifies process
// identity via /proc/{pid}/exe to avoid killing an unrelated process that
// reused the PID after a reboot.
func stopFallbackDaemon() error {
	pidFile, err := pkgconfig.DefaultPathManager().DaemonPIDFile()
	if err != nil {
		return fmt.Errorf("daemon pid file: %w", err)
	}
	pidData, err := os.ReadFile(pidFile) // #nosec G304: not user input
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read pid file: %w", err)
	}

	pid := strings.TrimSpace(string(pidData))
	if _, err := strconv.Atoi(pid); err != nil {
		// Corrupt PID file — clean up and move on
		_ = os.Remove(pidFile)
		return nil
	}

	return killDaemonIfOurs(pidFile, pid)
}

func killDaemonIfOurs(pidFile, pid string) error {
	running, err := command.IsRunning(pid)
	if err != nil || !running {
		// Process gone or check failed — stale PID file
		_ = os.Remove(pidFile)
		return nil
	}

	// Verify this is actually our daemon by checking the executable path.
	// After a reboot the PID may belong to an unrelated process.
	if !isDaemonProcess(pid) {
		_ = os.Remove(pidFile)
		return nil
	}

	if err := command.Kill(pid); err != nil {
		return fmt.Errorf("kill fallback daemon (pid %s): %w", pid, err)
	}

	_ = os.Remove(pidFile)
	return nil
}

// isDaemonProcess checks whether the process with the given PID is a Devsy
// daemon by reading /proc/{pid}/exe and verifying it matches our binary name.
func isDaemonProcess(pid string) bool {
	exePath, err := os.Readlink("/proc/" + pid + "/exe")
	if err != nil {
		// Can't verify — assume it's not ours to be safe
		return false
	}
	baseName := filepath.Base(exePath)
	// Handle " (deleted)" suffix when binary was replaced during upgrade
	baseName = strings.TrimSuffix(baseName, " (deleted)")
	return baseName == pkgconfig.BinaryName
}
