package up

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	devconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/encoding"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	sshconfig "github.com/devsy-org/devsy/pkg/ssh"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	safeRecreateRootPath  = "/root/devsy-safe-recreate-marker"
	safeRecreateVolumeDir = "/safe-recreate-volume"
	safeRecreateMarker    = "safe-recreate-preserved"
	safeRecreateSource    = "safe-recreate-source"
	safeRecreateConfig    = ".devcontainer.json"
	safeRecreateMemoryErr = "max_memory 1536 MiB must be greater than or equal to memory 2048 MiB"
)

// These request-invalid cases are independent of the separate managed-policy
// override gate; they do not establish that broader effective-policy guarantee.
var _ = ginkgo.Describe("MicroSandbox rejected replacement preservation",
	ginkgo.Label("up-provider-microsandbox-recreate"), func() {
		ginkgo.DescribeTable(
			"keeps the original generation until valid explicit replacement",
			func(ctx context.Context, source, name string, stopped bool) {
				fixture := newSafeRecreateFixture(ctx, source, name)
				fixture.initialize(ctx, stopped)
				fixture.rejectInvalidReplacement(ctx)
				fixture.inspectOriginalFiles(ctx, stopped)
				fixture.replaceSuccessfully(ctx)
			},
			ginkgo.Entry(
				"builtin running",
				microsandboxResourceBuiltin,
				"safe-recreate-builtin-running",
				false,
				ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
			ginkgo.Entry(
				"builtin stopped",
				microsandboxResourceBuiltin,
				"safe-recreate-builtin-stopped",
				true,
				ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
			ginkgo.Entry(
				"external running",
				microsandboxExternalProvider,
				"safe-recreate-external-running",
				false,
				ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
			ginkgo.Entry(
				"external stopped",
				microsandboxExternalProvider,
				"safe-recreate-external-stopped",
				true,
				ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
		)
	})

type safeRecreateFixture struct {
	framework                      *framework.Framework
	workspace, sandbox, uid, label string
	volume                         string
	volumeOwned                    bool
	original                       safeRecreateInspection
	bootID                         string
	record                         []byte
	sshFiles                       map[string]safeRecreateFile
	workspaceInfo                  *providerpkg.Workspace
	source                         safeRecreateFile
}

type safeRecreateInspection struct {
	CreatedAt    string          `json:"created_at"`
	Status       string          `json:"status"`
	ActiveConfig json.RawMessage `json:"active_config"`
	Config       json.RawMessage `json:"config"`
}

type safeRecreateFile struct {
	Data  []byte
	Mode  os.FileMode
	Owner string
}

func newSafeRecreateFixture(ctx context.Context, source, name string) *safeRecreateFixture {
	skipIfNoMicrosandbox(ctx)
	initialDir, err := os.Getwd()
	framework.ExpectNoError(err)
	home := ginkgo.GinkgoT().TempDir()
	ginkgo.GinkgoT().Setenv("HOME", home)
	ginkgo.GinkgoT().Setenv("USERPROFILE", home)
	ginkgo.GinkgoT().Setenv("DEVSY_HOME", ginkgo.GinkgoT().TempDir())
	ginkgo.GinkgoT().Setenv("DEVSY_CONFIG", "")
	ginkgo.GinkgoT().Setenv("MSB_HOME", filepath.Join(home, "microsandbox"))
	ginkgo.GinkgoT().Setenv("MSB_CONFIG_PATH", filepath.Join(home, "microsandbox", "config.json"))
	workspace, err := framework.CreateTempDir()
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, workspace)
	f := framework.NewDefaultFramework(filepath.Join(initialDir, "bin"))
	framework.ExpectNoError(f.DevsyProviderAdd(ctx, source, "--name", name))
	ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(cleanupCtx), time.Minute)
		defer cancel()
		return f.DevsyProviderDelete(cleanupCtx, name)
	})
	framework.ExpectNoError(f.DevsyProviderUse(ctx, name,
		microsandboxResourceOption, "MICROSANDBOX_MEMORY=0",
		microsandboxResourceOption, "MICROSANDBOX_MAX_MEMORY=1536",
		microsandboxResourceOption, "MICROSANDBOX_CPUS=1",
		microsandboxResourceOption, "MICROSANDBOX_MAX_CPUS=1",
		microsandboxResourceOption, "MICROSANDBOX_STORAGE=2",
		microsandboxResourceOption, "MICROSANDBOX_EPHEMERAL=false"))
	fixture := &safeRecreateFixture{framework: f, workspace: workspace}
	fixture.uid = encoding.CreateNewUID("", filepath.Base(workspace))
	fixture.sandbox = "devsy-" + fixture.uid
	fixture.label = "devsy.safe-recreate=" + fixture.uid
	fixture.volume = "devsy-safe-recreate-" + fixture.uid
	assertMicrosandboxEphemeralAbsent(ctx, fixture.label)
	out, err := microsandboxResourceCommand(ctx, "list", "--quiet")
	framework.ExpectNoError(err, string(out))
	gomega.Expect(strings.Fields(string(out))).NotTo(gomega.ContainElement(fixture.sandbox))
	fixture.registerCleanup()
	out, err = microsandboxResourceCommand(ctx, "volume", "create", fixture.volume)
	framework.ExpectNoError(err, string(out))
	fixture.volumeOwned = true
	return fixture
}

func (f *safeRecreateFixture) registerCleanup() {
	// Independent LIFO callbacks attempt workspace/backend cleanup before volume removal.
	ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
		if !f.volumeOwned {
			return nil
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(cleanupCtx), time.Minute)
		defer cancel()
		out, err := microsandboxResourceCommand(cleanupCtx, "volume", "rm", f.volume)
		if err != nil {
			return fmt.Errorf("remove owned replacement volume: %w: %s", err, out)
		}
		return nil
	})
	ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(cleanupCtx), time.Minute)
		defer cancel()
		out, err := microsandboxResourceCommand(cleanupCtx, "list", "--quiet")
		if err != nil {
			return fmt.Errorf("list owned replacement sandbox: %w: %s", err, out)
		}
		for name := range strings.FieldsSeq(string(out)) {
			if name == f.sandbox {
				out, err = microsandboxResourceCommand(cleanupCtx, "remove", "--force", name)
				if err != nil {
					return fmt.Errorf("remove owned replacement sandbox: %w: %s", err, out)
				}
			}
		}
		return nil
	})
	ginkgo.DeferCleanup(f.framework.CleanupWorkspace, f.workspace)
}

func (f *safeRecreateFixture) upArgs() []string {
	return []string{"--devcontainer", safeRecreateConfig, "--uid", f.uid, "--id-label", f.label}
}

func (f *safeRecreateFixture) writeConfig(memory string) {
	configuration := struct {
		Image            string                      `json:"image"`
		ContainerUser    string                      `json:"containerUser"`
		RemoteUser       string                      `json:"remoteUser"`
		Mounts           []string                    `json:"mounts"`
		HostRequirements *devconfig.HostRequirements `json:"hostRequirements"`
	}{
		Image:         microsandboxResourceImage,
		ContainerUser: microsandboxRootUser,
		RemoteUser:    microsandboxRootUser,
		Mounts: []string{
			"type=volume,source=" + f.volume + ",target=" + safeRecreateVolumeDir,
		},
		HostRequirements: &devconfig.HostRequirements{Memory: memory},
	}
	data, err := json.Marshal(configuration)
	framework.ExpectNoError(err)
	framework.ExpectNoError(
		os.WriteFile(filepath.Join(f.workspace, safeRecreateConfig), data, 0o600),
	)
}

func (f *safeRecreateFixture) initialize(ctx context.Context, stopped bool) {
	f.writeConfig("1gb")
	framework.ExpectNoError(
		os.WriteFile(
			filepath.Join(f.workspace, safeRecreateSource),
			[]byte(safeRecreateMarker),
			0o600,
		),
	)
	f.source = snapshotSafeRecreateFile(ctx, filepath.Join(f.workspace, safeRecreateSource))
	framework.ExpectNoError(
		f.framework.DevsyUp(ctx, append([]string{f.workspace}, f.upArgs()...)...),
	)
	workspace, err := f.framework.FindWorkspace(ctx, f.workspace)
	framework.ExpectNoError(err)
	gomega.Expect(workspace.UID).To(gomega.Equal(f.uid))
	gomega.Expect(microsandboxResourceSandbox(ctx, f.framework, f.workspace)).
		To(gomega.Equal(f.sandbox))
	f.workspaceInfo = workspace
	out, err := safeRecreateExec(ctx, f.sandbox, "sh", "-c",
		`printf '%s' "$1" > "$2"; printf '%s' "$1" > "$3"`, "sh", safeRecreateMarker,
		safeRecreateRootPath, safeRecreateVolumeDir+"/marker")
	framework.ExpectNoError(err, string(out))
	f.bootID = microsandboxStorageBootID(ctx, f.framework, f.workspace)
	gomega.Expect(microsandboxActiveResources(ctx, f.sandbox)).
		To(gomega.Equal(microsandboxResources{
			CPUs: 1, MemoryMiB: 1024, MaxCPUs: 1, MaxMemoryMiB: 1536,
		}))
	if stopped {
		framework.ExpectNoError(f.framework.DevsyWorkspaceStop(ctx, f.workspace))
	}
	f.original = inspectSafeRecreate(ctx, f.sandbox)
	gomega.Expect(f.original.CreatedAt).NotTo(gomega.BeEmpty())
	gomega.Expect(f.original.Status).
		To(gomega.Equal(map[bool]string{false: "Running", true: "Stopped"}[stopped]))
	f.record = snapshotSafeRecreateRecord(workspace)
	f.sshFiles = snapshotSafeRecreateSSH(ctx, workspace)
}

func (f *safeRecreateFixture) rejectInvalidReplacement(ctx context.Context) {
	f.writeConfig("2gb")
	stdout, stderr, err := f.framework.DevsyUpStreams(
		ctx,
		f.workspace,
		append(f.upArgs(), "--recreate")...)
	gomega.Expect(err).To(gomega.HaveOccurred(), "stdout: %s stderr: %s", stdout, stderr)
	gomega.Expect(stdout + stderr).To(gomega.ContainSubstring(safeRecreateMemoryErr))
	// Inspect status before any SSH/start, especially for an originally stopped VM.
	gomega.Expect(inspectSafeRecreate(ctx, f.sandbox)).To(gomega.Equal(f.original))
	f.assertHostState(ctx)
}

func (f *safeRecreateFixture) assertHostState(ctx context.Context) {
	current, err := f.framework.FindWorkspace(ctx, f.workspace)
	framework.ExpectNoError(err)
	gomega.Expect(current.ID).To(gomega.Equal(f.workspaceInfo.ID))
	gomega.Expect(current.UID).To(gomega.Equal(f.workspaceInfo.UID))
	gomega.Expect(current.Provider).To(gomega.Equal(f.workspaceInfo.Provider))
	gomega.Expect(current.CreationTimestamp).To(gomega.Equal(f.workspaceInfo.CreationTimestamp))
	gomega.Expect(snapshotSafeRecreateRecord(current)).To(gomega.Equal(f.record))
	gomega.Expect(snapshotSafeRecreateSSH(ctx, current)).To(gomega.Equal(f.sshFiles))
	gomega.Expect(snapshotSafeRecreateFile(ctx, filepath.Join(f.workspace, safeRecreateSource))).
		To(gomega.Equal(f.source))
}

func (f *safeRecreateFixture) inspectOriginalFiles(ctx context.Context, stopped bool) {
	f.writeConfig("1gb")
	if stopped {
		// The preceding refusal assertion already proved the old VM remained stopped.
		framework.ExpectNoError(
			f.framework.DevsyUp(ctx, append([]string{f.workspace}, f.upArgs()...)...),
		)
		gomega.Expect(inspectSafeRecreate(ctx, f.sandbox).CreatedAt).
			To(gomega.Equal(f.original.CreatedAt))
		gomega.Expect(microsandboxStorageBootID(ctx, f.framework, f.workspace)).
			NotTo(gomega.Equal(f.bootID))
		f.bootID = microsandboxStorageBootID(ctx, f.framework, f.workspace)
	} else {
		gomega.Expect(microsandboxStorageBootID(ctx, f.framework, f.workspace)).
			To(gomega.Equal(f.bootID))
	}
	out, err := safeRecreateExec(ctx, f.sandbox, "cat", safeRecreateRootPath)
	framework.ExpectNoError(err)
	gomega.Expect(string(out)).To(gomega.Equal(safeRecreateMarker))
	f.assertPersistentMounts(ctx)
	f.assertHostState(ctx)
}

func (f *safeRecreateFixture) assertPersistentMounts(ctx context.Context) {
	for _, path := range []string{
		safeRecreateVolumeDir + "/marker",
		filepath.ToSlash(filepath.Join("/workspaces", filepath.Base(f.workspace), safeRecreateSource)),
	} {
		out, err := safeRecreateExec(ctx, f.sandbox, "cat", path)
		framework.ExpectNoError(err)
		gomega.Expect(string(out)).To(gomega.Equal(safeRecreateMarker))
	}
}

func (f *safeRecreateFixture) replaceSuccessfully(ctx context.Context) {
	f.writeConfig("1536mb")
	framework.ExpectNoError(
		f.framework.DevsyUpRecreate(ctx, append([]string{f.workspace}, f.upArgs()...)...),
	)
	after := inspectSafeRecreate(ctx, f.sandbox)
	gomega.Expect(after.Status).To(gomega.Equal("Running"))
	gomega.Expect(after.CreatedAt).NotTo(gomega.Equal(f.original.CreatedAt))
	gomega.Expect(microsandboxStorageBootID(ctx, f.framework, f.workspace)).
		NotTo(gomega.Equal(f.bootID))
	out, err := safeRecreateExec(
		ctx,
		f.sandbox,
		"sh",
		"-c",
		`test ! -e "$1"`,
		"sh",
		safeRecreateRootPath,
	)
	framework.ExpectNoError(err, string(out))
	f.assertPersistentMounts(ctx)
	f.assertHostState(ctx)
	expected := microsandboxResources{CPUs: 1, MemoryMiB: 1536, MaxCPUs: 1, MaxMemoryMiB: 1536}
	gomega.Expect(microsandboxActiveResources(ctx, f.sandbox)).To(gomega.Equal(expected))
	assertMicrosandboxGuestResources(ctx, f.framework, f.workspace, expected)
}

func inspectSafeRecreate(ctx context.Context, sandbox string) safeRecreateInspection {
	out, err := microsandboxResourceCommand(
		ctx,
		"inspect",
		sandbox,
		"--format",
		microsandboxResourceJSON,
	)
	framework.ExpectNoError(err, string(out))
	var inspection safeRecreateInspection
	framework.ExpectNoError(json.Unmarshal(out, &inspection))
	return inspection
}

func safeRecreateExec(ctx context.Context, sandbox string, argv ...string) ([]byte, error) {
	args := append(
		[]string{"exec", "--stream", "--user", microsandboxRootUser, sandbox, "--"},
		argv...)
	// #nosec G204 -- Literal commands target only this fixture's exact owned VM.
	command := exec.CommandContext(ctx, "msb", args...)
	command.WaitDelay = 30 * time.Second
	return command.CombinedOutput()
}

func snapshotSafeRecreateFile(ctx context.Context, path string) safeRecreateFile {
	// #nosec G304 -- Source/SSH path is inside this fixture's isolated HOME or workspace.
	data, err := os.ReadFile(path)
	framework.ExpectNoError(err)
	info, err := os.Stat(path)
	framework.ExpectNoError(err)
	return safeRecreateFile{
		Data:  data,
		Mode:  info.Mode().Perm(),
		Owner: microsandboxHostOwner(ctx, path),
	}
}

func snapshotSafeRecreateRecord(workspace *providerpkg.Workspace) []byte {
	dir, err := providerpkg.GetWorkspaceDir(workspace.Context, workspace.ID)
	framework.ExpectNoError(err)
	// #nosec G304 -- Persisted record belongs to the fixture's owned workspace.
	data, err := os.ReadFile(filepath.Join(dir, providerpkg.WorkspaceConfigFile))
	framework.ExpectNoError(err)
	var record map[string]json.RawMessage
	framework.ExpectNoError(json.Unmarshal(data, &record))
	delete(record, "lastUsed")
	data, err = json.Marshal(record)
	framework.ExpectNoError(err)
	return data
}

func snapshotSafeRecreateSSH(
	ctx context.Context,
	workspace *providerpkg.Workspace,
) map[string]safeRecreateFile {
	paths := []string{workspace.SSHConfigPath}
	if workspace.SSHConfigIncludePath != "" {
		paths = append(paths, workspace.SSHConfigIncludePath)
	}
	result := map[string]safeRecreateFile{}
	for _, raw := range paths {
		path, err := sshconfig.ResolveSSHConfigPath(raw)
		framework.ExpectNoError(err)
		result[path] = snapshotSafeRecreateFile(ctx, path)
	}
	return result
}
