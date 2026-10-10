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
	"github.com/devsy-org/devsy/pkg/devcontainer"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	microsandboxResourceBuiltin = "microsandbox"
	microsandboxResourceOption  = "--option"
	microsandboxResourceJSON    = "json"
	microsandboxResourceImage   = "ghcr.io/devsy-org/test-images/base:alpine"
)

type microsandboxResources struct {
	CPUs         int `json:"cpus"`
	MemoryMiB    int `json:"memory_mib"`
	MaxCPUs      int `json:"max_cpus"`
	MaxMemoryMiB int `json:"max_memory_mib"`
}

type microsandboxResourcePlan struct {
	Applied bool                         `json:"applied"`
	Changes []microsandboxResourceChange `json:"changes"`
}

type microsandboxResourceChange struct {
	Field       string `json:"field"`
	Disposition string `json:"disposition"`
}

type microsandboxResourceConfig struct {
	Resources microsandboxResources `json:"resources"`
}

var _ = ginkgo.Describe("microsandbox resource parity",
	ginkgo.Label("up-provider-microsandbox-resources"), func() {
		for _, provider := range []struct{ name, source string }{
			{"builtin-resources", microsandboxResourceBuiltin},
			{"external-resources", microsandboxExternalProvider},
		} {
			ginkgo.Context(provider.name, func() {
				var f *framework.Framework
				var workspace string

				ginkgo.BeforeEach(func(ctx context.Context) {
					skipIfNoMicrosandbox(ctx)
					ginkgo.GinkgoT().Setenv("DEVSY_HOME", ginkgo.GinkgoT().TempDir())
					ginkgo.GinkgoT().Setenv("DEVSY_CONFIG", "")
					initialDir, err := os.Getwd()
					framework.ExpectNoError(err)
					f = framework.NewDefaultFramework(filepath.Join(initialDir, "bin"))
					workspace, err = framework.CreateTempDir()
					framework.ExpectNoError(err)
					ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, workspace)
					framework.ExpectNoError(
						f.DevsyProviderAdd(ctx, provider.source, "--name", provider.name),
					)
					ginkgo.DeferCleanup(f.DevsyProviderDelete, provider.name)
					ginkgo.DeferCleanup(f.CleanupWorkspace, workspace)
				})

				ginkgo.DescribeTable(
					"boots with the requested effective resources",
					func(
						ctx context.Context,
						options []string,
						requirements *config.HostRequirements,
						expected microsandboxResources,
					) {
						framework.ExpectNoError(f.DevsyProviderUse(ctx, provider.name, options...))
						writeMicrosandboxResourceConfig(workspace, requirements)
						framework.ExpectNoError(
							f.DevsyUp(ctx, workspace, "--devcontainer", ".devcontainer.json"),
						)
						sandbox := microsandboxResourceSandbox(ctx, f, workspace)
						gomega.Expect(microsandboxActiveResources(ctx, sandbox)).
							To(gomega.Equal(expected))
						assertMicrosandboxGuestResources(ctx, f, workspace, expected)
					},
					ginkgo.Entry(
						"provider defaults",
						nil,
						nil,
						microsandboxResources{
							CPUs:         1,
							MemoryMiB:    2048,
							MaxCPUs:      1,
							MaxMemoryMiB: 2048,
						},
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
					ginkgo.Entry(
						"provider options override host requirements",
						[]string{
							microsandboxResourceOption,
							"MICROSANDBOX_MEMORY=1536",
							microsandboxResourceOption,
							"MICROSANDBOX_CPUS=2",
						},
						&config.HostRequirements{CPUs: 1, Memory: "1gb"},
						microsandboxResources{
							CPUs:         2,
							MemoryMiB:    1536,
							MaxCPUs:      2,
							MaxMemoryMiB: 1536,
						},
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
					ginkgo.Entry(
						"zero provider values delegate sizing to host requirements",
						[]string{
							microsandboxResourceOption,
							"MICROSANDBOX_MEMORY=0",
							microsandboxResourceOption,
							"MICROSANDBOX_CPUS=0",
						},
						&config.HostRequirements{CPUs: 2, Memory: "1gb"},
						microsandboxResources{
							CPUs:         2,
							MemoryMiB:    1024,
							MaxCPUs:      2,
							MaxMemoryMiB: 1024,
						},
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
				)

				ginkgo.It(
					"hotplugs within boot ceilings and refuses targets beyond them without replacement",
					func(ctx context.Context) {
						framework.ExpectNoError(f.DevsyProviderUse(
							ctx,
							provider.name,
							microsandboxResourceOption,
							"MICROSANDBOX_MEMORY=1024",
							microsandboxResourceOption,
							"MICROSANDBOX_CPUS=1",
							microsandboxResourceOption,
							"MICROSANDBOX_MAX_MEMORY=1536",
							microsandboxResourceOption,
							"MICROSANDBOX_MAX_CPUS=2",
						))
						writeMicrosandboxResourceConfig(workspace, nil)
						framework.ExpectNoError(
							f.DevsyUp(ctx, workspace, "--devcontainer", ".devcontainer.json"),
						)
						sandbox := microsandboxResourceSandbox(ctx, f, workspace)
						createdAt := microsandboxCreationTime(ctx, sandbox)
						expected := microsandboxResources{
							CPUs:         1,
							MemoryMiB:    1024,
							MaxCPUs:      2,
							MaxMemoryMiB: 1536,
						}
						gomega.Expect(microsandboxActiveResources(ctx, sandbox)).
							To(gomega.Equal(expected))
						assertMicrosandboxGuestResources(ctx, f, workspace, expected)
						bootID, err := f.DevsySSHOnce(
							ctx,
							workspace,
							"cat /proc/sys/kernel/random/boot_id",
						)
						framework.ExpectNoError(err)
						gomega.Expect(strings.TrimSpace(bootID)).NotTo(gomega.BeEmpty())
						_, err = f.DevsySSHOnce(
							ctx,
							workspace,
							"printf 'preserved\\n' > /tmp/resource-parity-marker",
						)
						framework.ExpectNoError(err)

						// Devsy has no resize API: exercise the boot capacity via the test-owned backend VM.
						out, err := microsandboxResourceCommand(
							ctx,
							"modify",
							sandbox,
							"--cpus",
							"2",
							"--memory",
							"1536M",
							"--format",
							microsandboxResourceJSON,
						)
						framework.ExpectNoError(err, string(out))
						assertMicrosandboxResourcePlan(out, true, "live")
						expected.CPUs, expected.MemoryMiB = 2, 1536
						assertMicrosandboxGuestResources(ctx, f, workspace, expected)
						gomega.Expect(microsandboxActiveResources(ctx, sandbox)).
							To(gomega.Equal(expected))

						for _, target := range [][]string{{"--cpus", "3"}, {"--memory", "2048M"}} {
							args := append(
								[]string{"modify", sandbox, "--format", microsandboxResourceJSON},
								target...)
							out, err = microsandboxResourceCommand(ctx, args...)
							gomega.Expect(err).To(gomega.HaveOccurred(), string(out))
							assertMicrosandboxResourcePlan(out, false, "requires restart")
							gomega.Expect(microsandboxActiveResources(ctx, sandbox)).
								To(gomega.Equal(expected))
						}
						assertMicrosandboxGuestResources(ctx, f, workspace, expected)
						gomega.Expect(microsandboxCreationTime(ctx, sandbox)).
							To(gomega.Equal(createdAt))
						afterBootID, err := f.DevsySSHOnce(
							ctx,
							workspace,
							"cat /proc/sys/kernel/random/boot_id",
						)
						framework.ExpectNoError(err)
						gomega.Expect(afterBootID).To(gomega.Equal(bootID))
						outMarker, err := f.DevsySSHOnce(
							ctx,
							workspace,
							"cat /tmp/resource-parity-marker",
						)
						framework.ExpectNoError(err)
						gomega.Expect(outMarker).To(gomega.Equal("preserved\n"))
					},
					ginkgo.SpecTimeout(framework.TimeoutLong()),
				)

				ginkgo.DescribeTable("rejects boot ceilings below effective resources",
					func(ctx context.Context, options []string, diagnostic string) {
						framework.ExpectNoError(f.DevsyProviderUse(ctx, provider.name, options...))
						writeMicrosandboxResourceConfig(workspace, nil)
						stdout, stderr, err := f.DevsyUpStreams(
							ctx,
							workspace,
							"--devcontainer",
							".devcontainer.json",
						)
						gomega.Expect(err).To(gomega.HaveOccurred())
						gomega.Expect(stdout + stderr).To(gomega.ContainSubstring(diagnostic))
					},
					ginkgo.Entry(
						"CPU ceiling",
						[]string{
							microsandboxResourceOption,
							"MICROSANDBOX_CPUS=2",
							microsandboxResourceOption,
							"MICROSANDBOX_MAX_CPUS=1",
						},
						"max_cpus 1 must be greater than or equal to cpus 2",
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
					ginkgo.Entry(
						"memory ceiling",
						[]string{
							microsandboxResourceOption,
							"MICROSANDBOX_MEMORY=1024",
							microsandboxResourceOption,
							"MICROSANDBOX_MAX_MEMORY=512",
						},
						"max_memory 512 MiB must be greater than or equal to memory 1024 MiB",
						ginkgo.SpecTimeout(framework.TimeoutLong()),
					),
				)
			})
		}
	})

func writeMicrosandboxResourceConfig(dir string, requirements *config.HostRequirements) {
	devConfig := struct {
		Image            string                   `json:"image"`
		ContainerUser    string                   `json:"containerUser"`
		RemoteUser       string                   `json:"remoteUser"`
		HostRequirements *config.HostRequirements `json:"hostRequirements,omitempty"`
	}{
		Image:         microsandboxResourceImage,
		ContainerUser: microsandboxRootUser, RemoteUser: microsandboxRootUser,
		HostRequirements: requirements,
	}
	data, err := json.Marshal(devConfig)
	framework.ExpectNoError(err)
	framework.ExpectNoError(os.WriteFile(filepath.Join(dir, ".devcontainer.json"), data, 0o600))
}

func microsandboxResourceSandbox(
	ctx context.Context,
	f *framework.Framework,
	workspace string,
) string {
	info, err := f.FindWorkspace(ctx, workspace)
	framework.ExpectNoError(err)
	return "devsy-" + devcontainer.GetRunnerIDFromWorkspace(info)
}

func microsandboxActiveResources(ctx context.Context, sandbox string) microsandboxResources {
	out, err := microsandboxResourceCommand(
		ctx,
		"inspect",
		sandbox,
		"--format",
		microsandboxResourceJSON,
	)
	framework.ExpectNoError(err, string(out))
	var inspection struct {
		ActiveConfig *microsandboxResourceConfig `json:"active_config"`
	}
	framework.ExpectNoError(json.Unmarshal(out, &inspection), string(out))
	gomega.Expect(inspection.ActiveConfig).
		NotTo(gomega.BeNil(), "running VM must have active configuration")
	return inspection.ActiveConfig.Resources
}

func assertMicrosandboxGuestResources(
	ctx context.Context,
	f *framework.Framework,
	workspace string,
	expected microsandboxResources,
) {
	// The pinned runtime's live-resize tests allow 60s for guest convergence.
	pollCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	gomega.Eventually(pollCtx, func(g gomega.Gomega) {
		out, err := f.DevsySSHOnce(
			pollCtx,
			workspace,
			"printf '%s %s\\n' \"$(grep -c '^processor' /proc/cpuinfo)\" \"$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)\"",
		)
		if err != nil {
			gomega.StopTrying("read guest CPU and memory resources").Wrap(err).Now()
		}
		var cpus, memoryKiB int
		_, err = fmt.Sscanf(strings.TrimSpace(out), "%d %d", &cpus, &memoryKiB)
		if err != nil {
			gomega.StopTrying("parse guest CPU and memory resources").Wrap(err).Now()
		}
		g.Expect(cpus).To(gomega.Equal(expected.CPUs))
		// MemTotal excludes kernel reservations; keep a non-overlapping 128 MiB allowance.
		g.Expect(memoryKiB).To(gomega.BeNumerically(">", (expected.MemoryMiB-128)*1024))
		g.Expect(memoryKiB).To(gomega.BeNumerically("<=", expected.MemoryMiB*1024))
	}).WithTimeout(time.Minute).WithPolling(time.Second).Should(gomega.Succeed())
}

func assertMicrosandboxResourcePlan(out []byte, applied bool, disposition string) {
	var plan microsandboxResourcePlan
	framework.ExpectNoError(json.Unmarshal(out, &plan), string(out))
	gomega.Expect(plan.Applied).To(gomega.Equal(applied))
	resourceChanges := 0
	for _, change := range plan.Changes {
		if change.Field == "cpus" || change.Field == "memory" {
			resourceChanges++
			gomega.Expect(change.Disposition).To(gomega.Equal(disposition), change.Field)
		}
	}
	gomega.Expect(resourceChanges).To(gomega.BeNumerically(">", 0))
}

func microsandboxResourceCommand(ctx context.Context, args ...string) ([]byte, error) {
	// #nosec G204 -- fixed runtime executable and arguments for the test-owned VM.
	cmd := exec.CommandContext(ctx, "msb", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("msb %v: %w: %s", args, err, stderr.String())
	}
	return out, nil
}
