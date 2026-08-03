package mcp

import (
	"context"
	"os"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("devsy mcp serve", ginkgo.Label("mcp"), ginkgo.Ordered, func() {
	var initialDir string

	ginkgo.BeforeEach(func() {
		var err error
		initialDir, err = os.Getwd()
		framework.ExpectNoError(err)
	})

	ginkgo.It("lists a running workspace via workspace_list and execs a command via workspace_exec",
		func(ctx context.Context) {
			tempDir, f, err := setupWorkspaceAndUp(ctx, "tests/mcp/testdata/basic", initialDir)
			framework.ExpectNoError(err)

			client, err := f.StartMCPServer(ctx)
			framework.ExpectNoError(err)
			defer func() { _ = client.Close() }()

			listResult, isErr, err := client.CallTool(ctx, "workspace_list", map[string]any{})
			framework.ExpectNoError(err)
			gomega.Expect(isErr).To(gomega.BeFalse())
			workspaces, ok := listResult["workspaces"].([]any)
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(workspaces).NotTo(gomega.BeEmpty())

			execResult, isErr, err := client.CallTool(ctx, "workspace_exec", map[string]any{
				"name":    tempDir,
				"command": []string{"echo", "-n", "hello-from-mcp"},
			})
			framework.ExpectNoError(err)
			gomega.Expect(isErr).To(gomega.BeFalse())
			gomega.Expect(execResult["stdout"]).To(gomega.Equal("hello-from-mcp"))
			gomega.Expect(execResult["exit_code"]).To(gomega.BeNumerically("==", 0))
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))

	ginkgo.It("returns an isError result for an unknown workspace name",
		func(ctx context.Context) {
			f := framework.NewDefaultFramework(initialDir + "/bin")
			client, err := f.StartMCPServer(ctx)
			framework.ExpectNoError(err)
			defer func() { _ = client.Close() }()

			_, isErr, err := client.CallTool(ctx, "workspace_exec", map[string]any{
				"name":    "definitely-not-a-real-workspace",
				"command": []string{"echo", "hi"},
			})
			framework.ExpectNoError(err)
			gomega.Expect(isErr).To(gomega.BeTrue())
		}, ginkgo.SpecTimeout(framework.TimeoutShort()))
})
