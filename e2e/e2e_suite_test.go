package e2e

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	// Register tests.
	_ "github.com/devsy-org/devsy/e2e/tests/build"
	_ "github.com/devsy-org/devsy/e2e/tests/context"
	_ "github.com/devsy-org/devsy/e2e/tests/dockerinstall"
	_ "github.com/devsy-org/devsy/e2e/tests/ide"
	_ "github.com/devsy-org/devsy/e2e/tests/integration"
	_ "github.com/devsy-org/devsy/e2e/tests/machine"
	_ "github.com/devsy-org/devsy/e2e/tests/machineprovider"
	_ "github.com/devsy-org/devsy/e2e/tests/provider"
	_ "github.com/devsy-org/devsy/e2e/tests/ssh"
	_ "github.com/devsy-org/devsy/e2e/tests/up"
	_ "github.com/devsy-org/devsy/e2e/tests/up-features"
	_ "github.com/devsy-org/devsy/e2e/tests/upgrade"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

// TestRunE2ETests checks configuration parameters (specified through flags) and then runs
// E2E tests using the Ginkgo runner.
// If a "report directory" is specified, one or more JUnit test reports will be
// generated in this directory, and cluster logs will also be saved.
// This function is called on each Ginkgo node in parallel mode.
func TestRunE2ETests(t *testing.T) {
	if runtime.GOOS != "linux" {
		go framework.ServeAgent()

		// wait for http server to be up and running (max 30s)
		deadline := time.After(30 * time.Second)
		for {
			select {
			case <-deadline:
				t.Fatal("timeout waiting for DEVSY_AGENT_URL to be set after 30s")
			default:
			}
			time.Sleep(time.Second)
			if os.Getenv("DEVSY_AGENT_URL") != "" {
				break
			}
		}
	}
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Devsy e2e suite")
}
