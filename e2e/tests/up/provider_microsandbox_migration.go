package up

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	"github.com/devsy-org/devsy/pkg/encoding"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	microsandboxMigrationSuiteLabel = "up-provider-microsandbox-source-migration"
	microsandboxMigrationRootMarker = "/root/devsy-source-migration-marker"
	microsandboxMigrationVolumeDir  = "/source-migration-volume"
	microsandboxMigrationMarker     = "source-migration-preserved"
	microsandboxMigrationSource     = "source-migration-source"
)

var _ = ginkgo.Describe(
	"existing MicroSandbox workspace source migration",
	ginkgo.Label(microsandboxMigrationSuiteLabel),
	func() {
		ginkgo.DescribeTable(
			"migrates the existing provider without replacing its workspace or VM",
			func(ctx context.Context, stopped bool) {
				fixture := newMicrosandboxMigrationFixture(ctx)
				fixture.initialize(ctx, stopped)
				fixture.migrate(ctx, stopped)
				fixture.deleteWorkspace(ctx)
			},
			ginkgo.Entry(
				"running workspace",
				false,
				ginkgo.Label("microsandbox-external"),
				ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
			ginkgo.Entry(
				"stopped workspace",
				true,
				ginkgo.Label("microsandbox-external"),
				ginkgo.SpecTimeout(framework.TimeoutLong()),
			),
		)
	},
)

type microsandboxMigrationFixture struct {
	framework      *framework.Framework
	workspace      string
	sandbox        string
	uid            string
	label          string
	volume         string
	volumeOwned    bool
	workspaceState compatibilityWorkspaceState
	vmState        safeRecreateInspection
	bootID         string
	options        map[string]config.OptionValue
}

func newMicrosandboxMigrationFixture(
	ctx context.Context,
) *microsandboxMigrationFixture {
	session := newMicrosandboxTestSession(ctx)
	home := ginkgo.GinkgoT().TempDir()
	ginkgo.GinkgoT().Setenv("HOME", home)
	ginkgo.GinkgoT().Setenv("USERPROFILE", home)
	ginkgo.GinkgoT().Setenv("MSB_HOME", filepath.Join(home, "microsandbox"))
	ginkgo.GinkgoT().Setenv(
		"MSB_CONFIG_PATH",
		filepath.Join(home, "microsandbox", "config.json"),
	)

	workspace := session.newWorkspace()
	session.addProvider(ctx, microsandboxResourceBuiltin, "microsandbox")
	uid := encoding.CreateNewUID("", filepath.Base(workspace))
	fixture := &microsandboxMigrationFixture{
		framework: session.framework,
		workspace: workspace,
		uid:       uid,
		label:     "devsy.source-migration=" + uid,
		volume:    "devsy-source-migration-" + uid,
	}
	fixture.sandbox = "devsy-" + fixture.uid
	fixture.registerCleanup()

	framework.ExpectNoError(fixture.framework.DevsyProviderUse(ctx, "microsandbox",
		microsandboxResourceOption, "MICROSANDBOX_MEMORY=1024",
		microsandboxResourceOption, "MICROSANDBOX_STORAGE=2",
		microsandboxResourceOption, "MICROSANDBOX_EPHEMERAL=false"))
	assertMicrosandboxEphemeralAbsent(ctx, fixture.label)
	out, err := microsandboxResourceCommand(ctx, "list", "--quiet")
	framework.ExpectNoError(err, string(out))
	gomega.Expect(strings.Fields(string(out))).NotTo(gomega.ContainElement(fixture.sandbox))
	out, err = microsandboxResourceCommand(ctx, "volume", "create", fixture.volume)
	framework.ExpectNoError(err, string(out))
	fixture.volumeOwned = true
	fixture.writeConfig()

	return fixture
}

func (fixture *microsandboxMigrationFixture) registerCleanup() {
	// Cleanup is deliberately independent: a failed workspace deletion must not
	// prevent the exact owned sandbox fallback or named volume removal.
	ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
		if !fixture.volumeOwned {
			return nil
		}
		cleanupCtx, cancel := framework.DetachedCleanupContext(cleanupCtx, time.Minute)
		defer cancel()
		out, err := microsandboxResourceCommand(cleanupCtx, "volume", "rm", fixture.volume)
		if err != nil {
			return fmt.Errorf("remove owned migration volume: %w: %s", err, out)
		}
		return nil
	})
	ginkgo.DeferCleanup(func(cleanupCtx context.Context) error {
		cleanupCtx, cancel := framework.DetachedCleanupContext(cleanupCtx, time.Minute)
		defer cancel()
		out, err := microsandboxResourceCommand(cleanupCtx, "list", "--quiet")
		if err != nil {
			return fmt.Errorf("list migration sandbox for cleanup: %w: %s", err, out)
		}
		for name := range strings.FieldsSeq(string(out)) {
			if name == fixture.sandbox {
				out, err = microsandboxResourceCommand(cleanupCtx, "remove", "--force", name)
				if err != nil {
					return fmt.Errorf("remove owned migration sandbox: %w: %s", err, out)
				}
				break
			}
		}
		return nil
	})
	ginkgo.DeferCleanup(fixture.framework.CleanupWorkspace, fixture.workspace)
}

func (fixture *microsandboxMigrationFixture) writeConfig() {
	devcontainerConfig := struct {
		Image         string   `json:"image"`
		ContainerUser string   `json:"containerUser"`
		RemoteUser    string   `json:"remoteUser"`
		Mounts        []string `json:"mounts"`
	}{
		Image:         microsandboxResourceImage,
		ContainerUser: microsandboxRootUser,
		RemoteUser:    microsandboxRootUser,
		Mounts: []string{
			"type=volume,source=" + fixture.volume + ",target=" + microsandboxMigrationVolumeDir,
		},
	}
	data, err := json.Marshal(devcontainerConfig)
	framework.ExpectNoError(err)
	framework.ExpectNoError(os.WriteFile(
		filepath.Join(fixture.workspace, ".devcontainer.json"), data, 0o600,
	))
	framework.ExpectNoError(os.WriteFile(
		filepath.Join(fixture.workspace, microsandboxMigrationSource),
		[]byte(microsandboxMigrationMarker), 0o600,
	))
}

func (fixture *microsandboxMigrationFixture) initialize(ctx context.Context, stopped bool) {
	assertMigrationProvider("microsandbox", true, providerpkg.MicrosandboxDriver, "microsandbox")
	devsyConfig, err := config.LoadConfig("", "")
	framework.ExpectNoError(err)
	gomega.Expect(devsyConfig.Current().DefaultProvider).To(gomega.Equal("microsandbox"))
	providerConfig := devsyConfig.Current().Providers["microsandbox"]
	gomega.Expect(providerConfig).NotTo(gomega.BeNil())
	gomega.Expect(providerConfig.Initialized).To(gomega.BeTrue())
	fixture.options = migrationOptions(providerConfig.Options)
	assertMigrationOptions(fixture.options)

	framework.ExpectNoError(fixture.framework.DevsyUp(
		ctx,
		fixture.workspace,
		"--devcontainer", ".devcontainer.json",
		"--uid", fixture.uid,
		"--id-label", fixture.label,
	))
	workspace, err := fixture.framework.FindWorkspace(ctx, fixture.workspace)
	framework.ExpectNoError(err)
	gomega.Expect(workspace.UID).To(gomega.Equal(fixture.uid))
	gomega.Expect(workspace.Provider.Name).To(gomega.Equal("microsandbox"))
	gomega.Expect("devsy-" + devcontainer.GetRunnerIDFromWorkspace(workspace)).
		To(gomega.Equal(fixture.sandbox))
	fixture.workspaceState = snapshotCompatibilityWorkspace(ctx, workspace)
	fixture.vmState = inspectSafeRecreate(ctx, fixture.sandbox)
	gomega.Expect(fixture.vmState.Status).To(gomega.Equal("Running"))
	_, err = fixture.framework.DevsySSHOnce(ctx, fixture.workspace,
		"printf '%s' '"+microsandboxMigrationMarker+"' > "+microsandboxMigrationRootMarker+
			" && printf '%s' '"+microsandboxMigrationMarker+"' > "+
			microsandboxMigrationVolumeDir+"/marker")
	framework.ExpectNoError(err)
	fixture.bootID = microsandboxStorageBootID(ctx, fixture.framework, fixture.workspace)
	if stopped {
		framework.ExpectNoError(fixture.framework.DevsyWorkspaceStop(ctx, fixture.workspace))
		fixture.vmState = inspectSafeRecreate(ctx, fixture.sandbox)
		gomega.Expect(fixture.vmState.Status).To(gomega.Equal("Stopped"))
	}
}

func (fixture *microsandboxMigrationFixture) migrate(ctx context.Context, stopped bool) {
	framework.ExpectNoError(fixture.framework.DevsyProviderSetSource(
		ctx, "microsandbox", microsandboxExternalProvider,
	))
	assertMigrationProvider("microsandbox", false, providerpkg.ExternalDriver,
		microsandboxExternalProvider)
	devsyConfig, err := config.LoadConfig("", "")
	framework.ExpectNoError(err)
	gomega.Expect(devsyConfig.Current().DefaultProvider).To(gomega.Equal("microsandbox"))
	providerConfig := devsyConfig.Current().Providers["microsandbox"]
	gomega.Expect(providerConfig).NotTo(gomega.BeNil())
	gomega.Expect(providerConfig.Initialized).To(gomega.BeTrue())
	gomega.Expect(providerConfig.InitError).To(gomega.BeEmpty())
	afterOptions := migrationOptions(providerConfig.Options)
	assertMigrationOptions(afterOptions)
	gomega.Expect(afterOptions).To(gomega.Equal(fixture.options))

	workspace, err := fixture.framework.FindWorkspace(ctx, fixture.workspace)
	framework.ExpectNoError(err)
	gomega.Expect(snapshotCompatibilityWorkspace(ctx, workspace)).
		To(gomega.Equal(fixture.workspaceState))
	vmAfterMigration := inspectSafeRecreate(ctx, fixture.sandbox)
	gomega.Expect(vmAfterMigration.CreatedAt).To(gomega.Equal(fixture.vmState.CreatedAt))
	if stopped {
		gomega.Expect(vmAfterMigration.Status).To(gomega.Equal("Stopped"))
	} else {
		gomega.Expect(vmAfterMigration.Status).To(gomega.Equal("Running"))
		gomega.Expect(microsandboxStorageBootID(ctx, fixture.framework, fixture.workspace)).
			To(gomega.Equal(fixture.bootID))
		fixture.assertMarkers(ctx)
	}

	framework.ExpectNoError(fixture.framework.DevsyUp(ctx, fixture.workspace))
	workspace, err = fixture.framework.FindWorkspace(ctx, fixture.workspace)
	framework.ExpectNoError(err)
	gomega.Expect(workspace.ID).To(gomega.Equal(fixture.workspaceState.ID))
	gomega.Expect(workspace.UID).To(gomega.Equal(fixture.uid))
	gomega.Expect(workspace.Provider.Name).To(gomega.Equal("microsandbox"))
	gomega.Expect(snapshotCompatibilityWorkspace(ctx, workspace)).
		To(gomega.Equal(fixture.workspaceState))
	vmAfterUp := inspectSafeRecreate(ctx, fixture.sandbox)
	gomega.Expect(vmAfterUp.Status).To(gomega.Equal("Running"))
	gomega.Expect(vmAfterUp.CreatedAt).To(gomega.Equal(fixture.vmState.CreatedAt))
	bootAfterUp := microsandboxStorageBootID(ctx, fixture.framework, fixture.workspace)
	if stopped {
		gomega.Expect(bootAfterUp).NotTo(gomega.Equal(fixture.bootID))
	} else {
		gomega.Expect(bootAfterUp).To(gomega.Equal(fixture.bootID))
	}
	fixture.assertMarkers(ctx)
}

func (fixture *microsandboxMigrationFixture) assertMarkers(ctx context.Context) {
	for _, path := range []string{
		microsandboxMigrationRootMarker,
		microsandboxMigrationVolumeDir + "/marker",
		filepath.ToSlash(filepath.Join("/workspaces", filepath.Base(fixture.workspace), microsandboxMigrationSource)),
	} {
		out, err := fixture.framework.DevsySSHOnce(ctx, fixture.workspace, "cat "+path)
		framework.ExpectNoError(err, out)
		gomega.Expect(strings.TrimSpace(out)).To(gomega.Equal(microsandboxMigrationMarker))
	}
}

func (fixture *microsandboxMigrationFixture) deleteWorkspace(ctx context.Context) {
	framework.ExpectNoError(fixture.framework.DevsyWorkspaceDelete(ctx, fixture.workspace))
	gomega.Expect(providerpkg.WorkspaceExists(config.DefaultContext, fixture.workspaceState.ID)).
		To(gomega.BeFalse(), "the migrated workspace record should be deleted")
	out, err := microsandboxResourceCommand(ctx, "list", "--quiet")
	framework.ExpectNoError(err, string(out))
	gomega.Expect(strings.Fields(string(out))).NotTo(gomega.ContainElement(fixture.sandbox))
}

func assertMigrationProvider(
	name string,
	internal bool,
	driver string,
	rawSource string,
) {
	providerConfig, err := providerpkg.LoadProviderConfig(config.DefaultContext, name)
	framework.ExpectNoError(err)
	gomega.Expect(providerConfig.Name).To(gomega.Equal(name))
	gomega.Expect(providerConfig.Source.Internal).To(gomega.Equal(internal))
	gomega.Expect(providerConfig.Source.Raw).To(gomega.Equal(rawSource))
	gomega.Expect(providerConfig.Agent.Driver).To(gomega.Equal(driver))
}

func migrationOptions(options map[string]config.OptionValue) map[string]config.OptionValue {
	shared := make(map[string]config.OptionValue, 3)
	for _, key := range []string{
		"MICROSANDBOX_MEMORY", "MICROSANDBOX_STORAGE", "MICROSANDBOX_EPHEMERAL",
	} {
		shared[key] = options[key]
	}
	return shared
}

func assertMigrationOptions(options map[string]config.OptionValue) {
	for key, value := range map[string]string{
		"MICROSANDBOX_MEMORY":    "1024",
		"MICROSANDBOX_STORAGE":   "2",
		"MICROSANDBOX_EPHEMERAL": "false",
	} {
		option, ok := options[key]
		gomega.Expect(ok).To(gomega.BeTrue(), "shared option %s must remain configured", key)
		gomega.Expect(option.Value).To(gomega.Equal(value), "shared option %s", key)
		gomega.Expect(option.UserProvided).To(gomega.BeTrue(), "shared option %s", key)
	}
}
