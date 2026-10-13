package up

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	"github.com/devsy-org/devsy/pkg/encoding"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	sshconfig "github.com/devsy-org/devsy/pkg/ssh"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	compatibilityRemoveCommand   = "remove"
	compatibilityExecCommand     = "exec"
	compatibilityDeleteCommand   = "delete"
	compatibilityImageCommand    = "image"
	compatibilityVersionFaultEnv = "DEVSY_TEST_MSB_VERSION_FAULT"
	compatibilityRealMSBEnv      = "DEVSY_TEST_MSB_REAL"
	compatibilityMSBLogEnv       = "DEVSY_TEST_MSB_LOG"
	compatibilityRootMarker      = "/root/devsy-compatibility-preserved.txt"
	compatibilityRootValue       = "preserved-before-version-fault"
	compatibilitySourceFile      = "compatibility-source-sentinel.txt"
	compatibilitySourceValue     = "host-bind-preserved-before-version-fault\n"
)

// Version faults preserve a real supported VM; they do not emulate historical CLI semantics.
var _ = ginkgo.Describe(
	"recreated MicroSandbox workspaces with an injected CLI version fault",
	ginkgo.Label("up-provider-microsandbox-compatibility"),
	func() {
		ginkgo.DescribeTable(
			"preserves the existing workspace until the supported CLI is restored",
			func(ctx context.Context, source, name string) {
				fixture := newCompatibilityFixture(ctx, source, name)
				for _, fault := range []string{"old", "malformed", "failing"} {
					fixture.rejectVersionFault(ctx, fault)
				}
				fixture.recover(ctx)
			},
			ginkgo.Entry("built-in", "microsandbox", "msb-compat-builtin",
				ginkgo.SpecTimeout(framework.TimeoutLong())),
			ginkgo.Entry(
				"external",
				microsandboxExternalProvider,
				"msb-compat-external",
				ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
		)
	},
)

type compatibilityFixture struct {
	framework                     *framework.Framework
	shim                          *compatibilityMSBShim
	hostPath, sourcePath, sandbox string
	uid, label                    string
	workspace                     *providerpkg.Workspace
	workspaceBefore               compatibilityWorkspaceState
	vmBefore                      compatibilityVMState
	sourceBefore                  compatibilityFileState
}

func newCompatibilityFixture(ctx context.Context, source, name string) *compatibilityFixture {
	if runtime.GOOS == "windows" {
		microsandboxUnavailable("the version shim uses a POSIX shell and covers native Linux/macOS")
	}
	skipIfNoMicrosandbox(ctx)
	realMSB := captureCompatibilityMSB()
	initialDir, err := os.Getwd()
	framework.ExpectNoError(err)
	ownedHome := ginkgo.GinkgoT().TempDir()
	ginkgo.GinkgoT().Setenv("DEVSY_HOME", ginkgo.GinkgoT().TempDir())
	ginkgo.GinkgoT().Setenv("DEVSY_CONFIG", "")
	// Pinned msb0.7.7 accepts an absent private config as an empty defaults patch.
	msbHome := filepath.Join(ownedHome, "microsandbox")
	ginkgo.GinkgoT().Setenv("MSB_HOME", msbHome)
	ginkgo.GinkgoT().Setenv("MSB_CONFIG_PATH", filepath.Join(msbHome, "config.json"))
	ginkgo.GinkgoT().Setenv("HOME", ownedHome)
	ginkgo.GinkgoT().Setenv("USERPROFILE", ownedHome)
	f := framework.NewDefaultFramework(filepath.Join(initialDir, "bin"))
	tempDir, err := framework.CopyToTempDir("tests/up/testdata/microsandbox")
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)
	framework.ExpectNoError(f.DevsyProviderAdd(ctx, source, "--name", name))
	ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(cleanupCtx), time.Minute)
		defer cancel()
		framework.ExpectNoError(f.DevsyProviderDelete(cleanupCtx, name))
	})
	shim := installCompatibilityMSBShim(ginkgo.GinkgoT(), realMSB, ownedHome)
	fixture := &compatibilityFixture{
		framework: f, shim: shim, hostPath: tempDir,
		uid: encoding.CreateNewUID("", filepath.Base(tempDir)),
	}
	fixture.sandbox = "devsy-" + fixture.uid
	fixture.label = "devsy.compatibility-parity=" + fixture.uid
	assertMicrosandboxEphemeralAbsent(ctx, fixture.label)
	out, err := microsandboxResourceCommand(ctx, "list", "--quiet")
	framework.ExpectNoError(err, string(out))
	gomega.Expect(strings.Fields(string(out))).NotTo(gomega.ContainElement(fixture.sandbox))
	fixture.registerCleanup()
	fixture.initialize(ctx)
	return fixture
}

func (fixture *compatibilityFixture) registerCleanup() {
	// LIFO: workspace, exact backend fallback, PATH, provider, then source directory.
	// Faults are cleared independently so a failed workspace cleanup cannot block fallback.
	ginkgo.DeferCleanup(fixture.shim.restoreEnvironment)
	ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(cleanupCtx), time.Minute)
		defer cancel()
		fixture.shim.clearVersionFault()
		out, err := microsandboxResourceCommand(cleanupCtx, "list", "--quiet")
		if err != nil {
			return fmt.Errorf("list owned compatibility sandbox: %w: %s", err, out)
		}
		for name := range strings.FieldsSeq(string(out)) {
			if name == fixture.sandbox {
				out, err = microsandboxResourceCommand(
					cleanupCtx,
					compatibilityRemoveCommand,
					"--force",
					name,
				)
				if err != nil {
					return fmt.Errorf("remove owned compatibility sandbox: %w: %s", err, out)
				}
			}
		}
		return nil
	})
	ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(cleanupCtx), time.Minute)
		defer cancel()
		fixture.shim.clearVersionFault()
		return fixture.framework.CleanupWorkspace(cleanupCtx, fixture.hostPath)
	})
}

func (fixture *compatibilityFixture) upArgs() []string {
	return []string{
		"--devcontainer",
		".devcontainer.json",
		"--uid",
		fixture.uid,
		"--id-label",
		fixture.label,
	}
}

func (fixture *compatibilityFixture) initialize(ctx context.Context) {
	fixture.sourcePath = filepath.Join(fixture.hostPath, compatibilitySourceFile)
	// #nosec G306 -- source sentinel belongs to this temporary E2E workspace.
	framework.ExpectNoError(
		os.WriteFile(fixture.sourcePath, []byte(compatibilitySourceValue), 0o640),
	)
	// #nosec G302 -- The owned fixture verifies preservation of its original group-readable source mode.
	framework.ExpectNoError(os.Chmod(fixture.sourcePath, 0o640))
	fixture.sourceBefore = snapshotCompatibilityFile(ctx, fixture.sourcePath)
	framework.ExpectNoError(
		fixture.framework.DevsyUp(ctx, append([]string{fixture.hostPath}, fixture.upArgs()...)...),
	)
	workspace, err := fixture.framework.FindWorkspace(ctx, fixture.hostPath)
	framework.ExpectNoError(err)
	fixture.workspace = workspace
	gomega.Expect(workspace.UID).To(gomega.Equal(fixture.uid))
	gomega.Expect("devsy-" + devcontainer.GetRunnerIDFromWorkspace(workspace)).
		To(gomega.Equal(fixture.sandbox))
	_, err = compatibilityMSBExec(ctx, fixture.sandbox, "sh", "-c",
		`printf '%s' "$1" > "$2"`, "sh", compatibilityRootValue, compatibilityRootMarker)
	framework.ExpectNoError(err)
	fixture.workspaceBefore = snapshotCompatibilityWorkspace(ctx, workspace)
	fixture.vmBefore = snapshotCompatibilityVM(ctx, fixture.sandbox)
	gomega.Expect(fixture.vmBefore.Status).To(gomega.Equal("Running"))
	gomega.Expect(fixture.vmBefore.RootMarker).To(gomega.Equal(compatibilityRootValue))
}

func (fixture *compatibilityFixture) rejectVersionFault(ctx context.Context, fault string) {
	ginkgo.By("rejecting explicit recreation with an injected " + fault + " version response")
	fixture.shim.resetCalls()
	fixture.shim.setVersionFault(fault)
	stdout, stderr, err := fixture.framework.DevsyUpStreams(ctx, fixture.hostPath,
		append(fixture.upArgs(), "--recreate")...)
	gomega.Expect(err).To(gomega.HaveOccurred(), "stdout: %s\nstderr: %s", stdout, stderr)
	assertCompatibilityDiagnostic(fault, stdout+"\n"+stderr)
	calls := fixture.shim.calls()
	foundFault := false
	for _, call := range calls {
		if call.fault == fault {
			foundFault = true
			gomega.Expect(call.args).To(gomega.Equal([]string{"--version"}))
		}
	}
	gomega.Expect(foundFault).
		To(gomega.BeTrue(), "the failing operation must consume the injected version fault")
	assertNoCompatibilityMutation(calls)
	fixture.shim.clearVersionFault()
	fixture.assertPreservedState(ctx)
}

func assertCompatibilityDiagnostic(fault, output string) {
	// The external provider may fail in Info; builtin fails in provisioning.
	// Assert the underlying diagnostic without demanding identical RPC phases.
	switch fault {
	case "old":
		gomega.Expect(output).To(gomega.ContainSubstring("v0.7.2 or newer is required"))
	case "malformed":
		gomega.Expect(output).To(gomega.ContainSubstring("unable to parse microsandbox version"))
	case "failing":
		gomega.Expect(output).To(gomega.ContainSubstring("injected version command failure"))
	default:
		ginkgo.Fail("unknown compatibility fault: " + fault)
	}
}

func (fixture *compatibilityFixture) recover(ctx context.Context) {
	fixture.shim.clearVersionFault()
	fixture.shim.resetCalls()
	framework.ExpectNoError(fixture.framework.DevsyUpRecreate(
		ctx, append([]string{fixture.hostPath}, fixture.upArgs()...)...,
	))
	versionCalled := false
	for _, call := range fixture.shim.calls() {
		if len(call.args) == 1 && call.args[0] == "--version" {
			versionCalled = true
		}
	}
	gomega.Expect(versionCalled).To(gomega.BeTrue())
	fixture.shim.restoreEnvironment()
	after := snapshotCompatibilityVM(ctx, fixture.sandbox)
	gomega.Expect(after.Status).To(gomega.Equal("Running"))
	gomega.Expect(after.CreatedAt).NotTo(gomega.Equal(fixture.vmBefore.CreatedAt))
	gomega.Expect(after.BootID).NotTo(gomega.Equal(fixture.vmBefore.BootID))
	gomega.Expect(after.RootMarker).
		To(gomega.BeEmpty(), "successful replacement must discard the old root marker")
	fixture.assertRecord(ctx)
	assertCompatibilityFile(ctx, fixture.sourcePath, fixture.sourceBefore)
	assertCompatibilitySSH(ctx, fixture.workspace, fixture.workspaceBefore)
	assertCompatibilityGuestSource(ctx, fixture.sandbox, fixture.hostPath)
	sshCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := fixture.framework.DevsySSHOnce(
		sshCtx,
		fixture.hostPath,
		"printf '%s' compatibility-recovered",
	)
	framework.ExpectNoError(err)
	gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal("compatibility-recovered"))
}

type compatibilityTestT interface {
	Helper()
	TempDir() string
	Setenv(string, string)
}

type compatibilityMSBShim struct {
	test         compatibilityTestT
	logPath      string
	originalPath string
}

func captureCompatibilityMSB() string {
	path, err := exec.LookPath("msb")
	framework.ExpectNoError(err)
	path, err = filepath.Abs(path)
	framework.ExpectNoError(err)
	resolved, err := filepath.EvalSymlinks(path)
	framework.ExpectNoError(err)
	info, err := os.Stat(resolved)
	framework.ExpectNoError(err)
	gomega.Expect(info.Mode().IsRegular()).To(gomega.BeTrue())
	gomega.Expect(info.Mode().Perm() & 0o111).NotTo(gomega.BeZero())
	return resolved
}

func installCompatibilityMSBShim(t compatibilityTestT, realMSB, home string) *compatibilityMSBShim {
	t.Helper()
	originalPath := os.Getenv("PATH")
	shimDir := t.TempDir()
	shimPath := filepath.Join(shimDir, "msb")
	logPath := filepath.Join(shimDir, "invocations")
	framework.ExpectNoError(os.Mkdir(logPath, 0o700))
	script := `#!/bin/sh
set -eu
call_file=$(mktemp "$DEVSY_TEST_MSB_LOG/call.XXXXXX")
fault=""
if [ "$#" -eq 1 ] && [ "$1" = "--version" ]; then
  fault="${DEVSY_TEST_MSB_VERSION_FAULT-}"
fi
printf '%s\0' "$fault" "$@" > "$call_file"
if [ "$#" -eq 1 ] && [ "$1" = "--version" ]; then
  if [ -n "$fault" ]; then
    case "$fault" in
      old) printf 'msb 0.7.1\n'; exit 0 ;;
      malformed) printf 'msb not-a-version\n'; exit 0 ;;
      failing) printf '%s\n' 'injected version command failure' >&2; exit 42 ;;
      *) printf '%s\n' 'unknown test fault mode' >&2; exit 64 ;;
    esac
  fi
fi
exec "$DEVSY_TEST_MSB_REAL" "$@"
`
	// The executable path and arguments arrive through environment/argv, never
	// interpolated into this script; exec preserves exact argument boundaries.
	// #nosec G306 G703 -- private, test-owned executable shim.
	if err := os.WriteFile(shimPath, []byte(script), 0o700); err != nil {
		framework.ExpectNoError(err)
	}
	// #nosec G302 -- Only the owner may execute this private test shim.
	framework.ExpectNoError(os.Chmod(shimPath, 0o700))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+originalPath)
	t.Setenv(compatibilityRealMSBEnv, realMSB)
	t.Setenv(compatibilityMSBLogEnv, logPath)
	t.Setenv(compatibilityVersionFaultEnv, "")
	resolvedShim, err := exec.LookPath("msb")
	framework.ExpectNoError(err)
	gomega.Expect(filepath.Clean(resolvedShim)).To(gomega.Equal(filepath.Clean(shimPath)))
	return &compatibilityMSBShim{
		test: t, logPath: logPath, originalPath: originalPath,
	}
}

func (shim *compatibilityMSBShim) setVersionFault(fault string) {
	shim.test.Helper()
	if err := os.Setenv(compatibilityVersionFaultEnv, fault); err != nil {
		framework.ExpectNoError(err)
	}
}

func (shim *compatibilityMSBShim) clearVersionFault() {
	shim.test.Helper()
	if err := os.Setenv(compatibilityVersionFaultEnv, ""); err != nil {
		framework.ExpectNoError(err)
	}
}

func (shim *compatibilityMSBShim) restoreEnvironment() {
	shim.test.Helper()
	shim.clearVersionFault()
	if err := os.Setenv("PATH", shim.originalPath); err != nil {
		framework.ExpectNoError(err)
	}
}

type compatibilityMSBCall struct {
	fault string
	args  []string
}

func (shim *compatibilityMSBShim) resetCalls() {
	entries, err := os.ReadDir(shim.logPath)
	framework.ExpectNoError(err)
	for _, entry := range entries {
		framework.ExpectNoError(os.Remove(filepath.Join(shim.logPath, entry.Name())))
	}
}

func (shim *compatibilityMSBShim) calls() []compatibilityMSBCall {
	entries, err := os.ReadDir(shim.logPath)
	framework.ExpectNoError(err)
	calls := make([]compatibilityMSBCall, 0, len(entries))
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(shim.logPath, entry.Name()))
		framework.ExpectNoError(err)
		fields := bytes.Split(data, []byte{0})
		gomega.Expect(len(fields)).To(gomega.BeNumerically(">=", 3))
		gomega.Expect(fields[len(fields)-1]).
			To(gomega.BeEmpty(), "complete NUL-delimited argv record")
		call := compatibilityMSBCall{fault: string(fields[0])}
		for _, field := range fields[1 : len(fields)-1] {
			call.args = append(call.args, string(field))
		}
		calls = append(calls, call)
	}
	return calls
}

func assertNoCompatibilityMutation(calls []compatibilityMSBCall) {
	for _, call := range calls {
		command := call.args[0]
		mutations := []string{
			"create",
			"run",
			"start",
			"stop",
			compatibilityRemoveCommand,
			compatibilityDeleteCommand,
			"modify",
			compatibilityExecCommand,
		}
		gomega.Expect(mutations).
			NotTo(gomega.ContainElement(command), "unexpected mutation argv: %q", call.args)
		if command == "volume" || command == compatibilityImageCommand {
			gomega.Expect(len(call.args)).To(gomega.BeNumerically(">=", 2))
			gomega.Expect([]string{"list", "ls", "inspect"}).To(gomega.ContainElement(call.args[1]),
				"unexpected mutating subcommand argv: %q", call.args)
		}
	}
}

type compatibilityVMState struct {
	CreatedAt  string
	Status     string
	BootID     string
	RootMarker string
}

func snapshotCompatibilityVM(ctx context.Context, sandbox string) compatibilityVMState {
	// #nosec G204 -- fixed inspect command and test-owned sandbox identifier.
	inspect, err := exec.CommandContext(ctx, "msb", "inspect", sandbox, "--format", "json").Output()
	framework.ExpectNoError(err)
	var record struct {
		CreatedAt string `json:"created_at"`
		Status    string `json:"status"`
	}
	framework.ExpectNoError(json.Unmarshal(inspect, &record))
	bootID, err := compatibilityMSBExec(ctx, sandbox, "cat", "/proc/sys/kernel/random/boot_id")
	framework.ExpectNoError(err)
	root, err := compatibilityMSBExec(
		ctx,
		sandbox,
		"sh",
		"-c",
		"if [ -f \"$1\" ]; then cat \"$1\"; else printf '%s' missing; fi",
		"sh",
		compatibilityRootMarker,
	)
	framework.ExpectNoError(err)
	rootMarker := string(root)
	if rootMarker == "missing" {
		rootMarker = ""
	}
	return compatibilityVMState{
		CreatedAt:  record.CreatedAt,
		Status:     record.Status,
		BootID:     strings.TrimSpace(string(bootID)),
		RootMarker: rootMarker,
	}
}

func compatibilityMSBExec(ctx context.Context, sandbox string, argv ...string) ([]byte, error) {
	args := []string{compatibilityExecCommand, "--stream", sandbox, "--"}
	args = append(args, argv...)
	// #nosec G204 -- fixed executable and literal test-owned VM commands.
	cmd := exec.CommandContext(ctx, "msb", args...)
	cmd.WaitDelay = 30 * time.Second
	return cmd.CombinedOutput()
}

func (fixture *compatibilityFixture) assertPreservedState(ctx context.Context) {
	current := snapshotCompatibilityVM(ctx, fixture.sandbox)
	gomega.Expect(current).To(gomega.Equal(fixture.vmBefore))
	fixture.assertRecord(ctx)
	assertCompatibilityFile(ctx, fixture.sourcePath, fixture.sourceBefore)
	assertCompatibilitySSH(ctx, fixture.workspace, fixture.workspaceBefore)
	assertCompatibilityGuestSource(ctx, fixture.sandbox, fixture.hostPath)
}

func assertCompatibilityGuestSource(ctx context.Context, sandbox, hostPath string) {
	guestPath := filepath.ToSlash(
		filepath.Join("/workspaces", filepath.Base(hostPath), compatibilitySourceFile),
	)
	guest, err := compatibilityMSBExec(ctx, sandbox, "cat", guestPath)
	framework.ExpectNoError(err)
	gomega.Expect(string(guest)).To(gomega.Equal(compatibilitySourceValue))
}

type compatibilityFileState struct {
	Exists bool
	Bytes  []byte
	Mode   os.FileMode
	Owner  string
}

func snapshotCompatibilityFile(ctx context.Context, path string) compatibilityFileState {
	// #nosec G304 -- Path is a source or SSH file in this fixture's isolated HOME/workspace.
	data, err := os.ReadFile(path)
	framework.ExpectNoError(err)
	info, err := os.Stat(path)
	framework.ExpectNoError(err)
	return compatibilityFileState{
		Exists: true,
		Bytes:  data,
		Mode:   info.Mode().Perm(),
		Owner:  microsandboxHostOwner(ctx, path),
	}
}

func assertCompatibilityFile(ctx context.Context, path string, want compatibilityFileState) {
	actual := snapshotCompatibilityFile(ctx, path)
	gomega.Expect(actual).To(gomega.Equal(want))
}

type compatibilityWorkspaceState struct {
	ID                    string
	UID                   string
	ProviderName          string
	CreationTimestamp     string
	SSHConfigPath         string
	SSHConfigIncludePath  string
	RecordWithoutLastUsed []byte
	SSHFiles              map[string]compatibilityFileState
}

func snapshotCompatibilityWorkspace(
	ctx context.Context,
	workspace *providerpkg.Workspace,
) compatibilityWorkspaceState {
	dir, err := providerpkg.GetWorkspaceDir(workspace.Context, workspace.ID)
	framework.ExpectNoError(err)
	configPath := filepath.Join(dir, providerpkg.WorkspaceConfigFile)
	// #nosec G304 -- Config path is the persisted record for this fixture's owned workspace.
	data, err := os.ReadFile(configPath)
	framework.ExpectNoError(err)
	var record map[string]json.RawMessage
	framework.ExpectNoError(json.Unmarshal(data, &record))
	// A failed up attempt may legitimately update access time before the runtime
	// preflight. Compare every other persisted field for record identity/state.
	delete(record, "lastUsed")
	stableRecord, err := json.Marshal(record)
	framework.ExpectNoError(err)
	return compatibilityWorkspaceState{
		ID:                    workspace.ID,
		UID:                   workspace.UID,
		ProviderName:          workspace.Provider.Name,
		CreationTimestamp:     workspace.CreationTimestamp.String(),
		SSHConfigPath:         workspace.SSHConfigPath,
		SSHConfigIncludePath:  workspace.SSHConfigIncludePath,
		RecordWithoutLastUsed: stableRecord,
		SSHFiles:              snapshotCompatibilitySSHFiles(ctx, workspace),
	}
}

func (fixture *compatibilityFixture) assertRecord(ctx context.Context) {
	want := fixture.workspaceBefore
	current, err := fixture.framework.FindWorkspace(ctx, fixture.hostPath)
	framework.ExpectNoError(err)
	gomega.Expect(current.ID).To(gomega.Equal(want.ID))
	gomega.Expect(current.UID).To(gomega.Equal(want.UID))
	gomega.Expect(current.Provider.Name).To(gomega.Equal(want.ProviderName))
	gomega.Expect(current.CreationTimestamp.String()).To(gomega.Equal(want.CreationTimestamp))
	gomega.Expect(current.SSHConfigPath).To(gomega.Equal(want.SSHConfigPath))
	gomega.Expect(current.SSHConfigIncludePath).To(gomega.Equal(want.SSHConfigIncludePath))
	after := snapshotCompatibilityWorkspace(ctx, current)
	gomega.Expect(after.RecordWithoutLastUsed).To(gomega.Equal(want.RecordWithoutLastUsed))
	gomega.Expect(fixture.workspace.ID).To(gomega.Equal(want.ID))
}

func snapshotCompatibilitySSHFiles(
	ctx context.Context,
	workspace *providerpkg.Workspace,
) map[string]compatibilityFileState {
	paths := []string{workspace.SSHConfigPath}
	if workspace.SSHConfigPath == "" {
		paths[0] = ""
	}
	if workspace.SSHConfigIncludePath != "" {
		paths = append(paths, workspace.SSHConfigIncludePath)
	}
	result := make(map[string]compatibilityFileState, len(paths))
	for _, raw := range paths {
		path, err := sshconfig.ResolveSSHConfigPath(raw)
		framework.ExpectNoError(err)
		// #nosec G304 -- Path is a source or SSH file in this fixture's isolated HOME/workspace.
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			result[path] = compatibilityFileState{}
			continue
		}
		framework.ExpectNoError(err)
		info, err := os.Stat(path)
		framework.ExpectNoError(err)
		result[path] = compatibilityFileState{
			Exists: true,
			Bytes:  data,
			Mode:   info.Mode().Perm(),
			Owner:  microsandboxHostOwner(ctx, path),
		}
	}
	return result
}

func assertCompatibilitySSH(
	ctx context.Context,
	workspace *providerpkg.Workspace,
	want compatibilityWorkspaceState,
) {
	gomega.Expect(snapshotCompatibilitySSHFiles(ctx, workspace)).
		To(gomega.Equal(want.SSHFiles))
}
