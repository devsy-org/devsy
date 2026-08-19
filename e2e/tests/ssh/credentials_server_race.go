package ssh

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	cmdWorkspace = "workspace"
	cmdSSH       = "ssh"
)

var _ = ginkgo.Describe(
	"devsy ssh credentials server race",
	ginkgo.Label("ssh-credentials-server-race"),
	ginkgo.Ordered,
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It(
			"should not surface credentials-server port errors when two ssh sessions race for the same workspace",
			ginkgo.SpecTimeout(framework.TimeoutModerate()),
			func(ctx context.Context) {
				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/local-test")
				framework.ExpectNoError(err)

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_ = f.DevsyProviderAdd(ctx, "docker")
				err = f.DevsyProviderUse(ctx, "docker")
				framework.ExpectNoError(err)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				upDeadline := time.Now().Add(5 * time.Minute)
				upCtx, cancelUp := context.WithDeadline(ctx, upDeadline)
				defer cancelUp()
				err = f.DevsyUp(upCtx, tempDir)
				framework.ExpectNoError(err)

				const sessions = 2
				var wg sync.WaitGroup
				stderrs := make([]string, sessions)
				runErrs := make([]error, sessions)
				for i := range sessions {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						sshCtx, cancelSSH := context.WithDeadline(
							ctx,
							time.Now().Add(30*time.Second),
						)
						defer cancelSSH()
						_, stderr, sshErr := f.ExecCommandCapture(sshCtx, []string{
							cmdWorkspace, cmdSSH, tempDir, "--command", "sleep 2",
						})
						stderrs[i] = stderr
						runErrs[i] = sshErr
					}(i)
				}
				wg.Wait()

				for i := range sessions {
					framework.ExpectNoError(runErrs[i], "ssh session %d stderr: %s", i, stderrs[i])
					gomega.Expect(stderrs[i]).NotTo(gomega.ContainSubstring("not available"))
					gomega.Expect(stderrs[i]).NotTo(gomega.ContainSubstring("credentials server"))
				}
			},
		)
	},
)
