package machine

import (
	"context"
	"os"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/google/uuid"
	"github.com/onsi/ginkgo/v2"
)

var _ = ginkgo.Describe("devsy testing machine", ginkgo.Label("machine"), ginkgo.Ordered, func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It(
		"should add simple machine and then delete it",
		ginkgo.SpecTimeout(framework.TimeoutShort()),
		func(ctx context.Context) {
			tempDir, err := framework.CopyToTempDir("tests/machine/testdata")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			f := framework.NewDefaultFramework(initialDir + "/bin")

			// Ensure that mock-provider is deleted
			_ = f.DevsyProviderDelete(ctx, "mock-provider")

			ginkgo.By("Add mock provider")
			err = f.DevsyProviderAdd(ctx, tempDir+"/mock-provider.yaml")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
				err = f.DevsyProviderDelete(cleanupCtx, "mock-provider")
				framework.ExpectNoError(err)
			})

			ginkgo.By("Use mock provider")
			err = f.DevsyProviderUse(ctx, "mock-provider")
			framework.ExpectNoError(err)

			machineUUID, _ := uuid.NewRandom()
			machineName := machineUUID.String()

			ginkgo.By("Create test machine with mock provider")
			err = f.DevsyMachineCreate(ctx, []string{machineName})
			framework.ExpectNoError(err)

			ginkgo.By("Remove test machine")
			err = f.DevsyMachineDelete(ctx, []string{machineName})
			framework.ExpectNoError(err)
		},
	)

	ginkgo.It(
		"should delete a non-existing machine and get an error",
		ginkgo.SpecTimeout(framework.TimeoutShort()),
		func(ctx context.Context) {
			tempDir, err := framework.CopyToTempDir("tests/machine/testdata")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(framework.CleanupTempDir, initialDir, tempDir)

			f := framework.NewDefaultFramework(initialDir + "/bin")

			// Ensure that mock-provider is deleted
			_ = f.DevsyProviderDelete(ctx, "mock-provider")

			ginkgo.By("Add mock provider")
			err = f.DevsyProviderAdd(ctx, tempDir+"/mock-provider.yaml")
			framework.ExpectNoError(err)
			ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
				err = f.DevsyProviderDelete(cleanupCtx, "mock-provider")
				framework.ExpectNoError(err)
			})

			ginkgo.By("Use mock provider")
			err = f.DevsyProviderUse(ctx, "mock-provider")
			framework.ExpectNoError(err)

			machineUUID1, err := uuid.NewRandom()
			framework.ExpectNoError(err)
			machineName1 := machineUUID1.String()

			machineUUID2, err := uuid.NewRandom()
			framework.ExpectNoError(err)
			machineName2 := machineUUID2.String()

			ginkgo.By("Create test machine with mock provider")
			err = f.DevsyMachineCreate(ctx, []string{machineName1})
			framework.ExpectNoError(err)

			ginkgo.By("Remove existing test machine")
			err = f.DevsyMachineDelete(ctx, []string{machineName1})
			framework.ExpectNoError(err)

			ginkgo.By("Remove not existing test machine (should get an error)")
			err = f.DevsyMachineDelete(ctx, []string{machineName2})
			framework.ExpectError(err)
		},
	)
})
