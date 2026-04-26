package up

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
)

var _ = ginkgo.Describe(
	"testing up command with private repos",
	ginkgo.Label("up-private-token"),
	func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It("should allow checkout of a private GitRepo", func(ctx context.Context) {
			username := os.Getenv("GH_USERNAME")
			token := os.Getenv("GH_ACCESS_TOKEN")
			if username == "" || token == "" {
				ginkgo.Skip("GH_USERNAME and GH_ACCESS_TOKEN must be set")
			}

			// GitHub App tokens require "x-access-token" as the credential username
			credUser := os.Getenv("GH_CREDENTIAL_USERNAME")
			if credUser == "" {
				credUser = username
			}

			f, err := setupDockerProvider(initialDir+"/bin", "docker")
			framework.ExpectNoError(err)

			// Register credential cleanup before writing to ensure cleanup on any failure
			credentialPath := filepath.Join(os.Getenv("HOME"), ".git-credentials")
			ginkgo.DeferCleanup(func() { _ = os.Remove(credentialPath) })

			// setup git credentials
			err = exec.Command("git", []string{"config", "--global", "credential.helper", "store"}...).
				Run()
			framework.ExpectNoError(err)

			gitCredentialString := []byte("https://" + credUser + ":" + token + "@github.com")
			err = os.WriteFile(credentialPath, gitCredentialString, 0o600)
			framework.ExpectNoError(err)

			name := "testprivaterepo"
			ginkgo.DeferCleanup(f.DevsyWorkspaceDelete, name)

			// test repo must have .devcontainer.json to avoid MCR rate limits
			err = f.DevsyUp(ctx, "https://github.com/"+username+"/test_private_repo.git")
			framework.ExpectNoError(err)

			// verify forwarded credentials by cloning the private repo from within the container
			out, err := f.DevsySSH(
				ctx,
				name,
				"git clone https://github.com/"+username+"/test_private_repo",
			)
			framework.ExpectNoError(err)
			ginkgo.By(out)
		}, ginkgo.SpecTimeout(framework.GetTimeout()))
	},
)
