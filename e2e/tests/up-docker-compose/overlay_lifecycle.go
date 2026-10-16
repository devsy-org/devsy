//go:build linux || darwin || unix

package up

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	"github.com/docker/docker/api/types/container"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

func (tc *testContext) verifyMissingComposeOverride(
	ctx context.Context,
	workspace string,
	before *container.InspectResponse,
) {
	framework.ExpectNoError(tc.f.DevsyWorkspaceStop(ctx, workspace))
	var override string
	for file := range strings.SplitSeq(before.Config.Labels[pkgconfig.ComposeConfigFilesLabel], ",") {
		if strings.HasPrefix(filepath.Base(file), devcontainer.FeaturesStartOverrideFilePrefix) {
			override = file
			break
		}
	}
	gomega.Expect(override).NotTo(gomega.BeEmpty())
	// #nosec G304 -- this override belongs to the test-owned workspace.
	contents, err := os.ReadFile(override)
	framework.ExpectNoError(err)
	ginkgo.DeferCleanup(func() {
		// #nosec G703 -- restore only the generated override of the test-owned workspace.
		framework.ExpectNoError(os.WriteFile(override, contents, 0o600))
	})
	framework.ExpectNoError(os.Remove(override))
	_, err = tc.f.DevsySSHOnce(ctx, workspace, "true")
	gomega.Expect(err).To(gomega.HaveOccurred())
	gomega.Expect(err.Error()).To(gomega.ContainSubstring("saved project files are unavailable"))
	after, err := tc.inspectContainer(ctx, []string{before.ID})
	framework.ExpectNoError(err)
	gomega.Expect(after.ID).To(gomega.Equal(before.ID))
	gomega.Expect(after.Image).To(gomega.Equal(before.Image))
	gomega.Expect(after.State.Running).To(gomega.BeFalse())
}
