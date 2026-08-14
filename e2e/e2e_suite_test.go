package e2e

import (
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	// Register tests.
	_ "github.com/devsy-org/devsy/e2e/tests/build"
	_ "github.com/devsy-org/devsy/e2e/tests/ci"
	_ "github.com/devsy-org/devsy/e2e/tests/configapply"
	_ "github.com/devsy-org/devsy/e2e/tests/configread"
	_ "github.com/devsy-org/devsy/e2e/tests/context"
	_ "github.com/devsy-org/devsy/e2e/tests/delivery"
	_ "github.com/devsy-org/devsy/e2e/tests/dockerinstall"
	_ "github.com/devsy-org/devsy/e2e/tests/exec"
	_ "github.com/devsy-org/devsy/e2e/tests/extends"
	_ "github.com/devsy-org/devsy/e2e/tests/extends-up"
	_ "github.com/devsy-org/devsy/e2e/tests/feature"
	_ "github.com/devsy-org/devsy/e2e/tests/ide"
	_ "github.com/devsy-org/devsy/e2e/tests/integration"
	_ "github.com/devsy-org/devsy/e2e/tests/logs"
	_ "github.com/devsy-org/devsy/e2e/tests/machine"
	_ "github.com/devsy-org/devsy/e2e/tests/machineprovider"
	_ "github.com/devsy-org/devsy/e2e/tests/mcp"
	_ "github.com/devsy-org/devsy/e2e/tests/outdated"
	_ "github.com/devsy-org/devsy/e2e/tests/provider"
	_ "github.com/devsy-org/devsy/e2e/tests/rename"
	_ "github.com/devsy-org/devsy/e2e/tests/runusercommands"
	_ "github.com/devsy-org/devsy/e2e/tests/self"
	_ "github.com/devsy-org/devsy/e2e/tests/snapshot"
	_ "github.com/devsy-org/devsy/e2e/tests/ssh"
	_ "github.com/devsy-org/devsy/e2e/tests/template"
	_ "github.com/devsy-org/devsy/e2e/tests/tunnel"
	_ "github.com/devsy-org/devsy/e2e/tests/up"
	_ "github.com/devsy-org/devsy/e2e/tests/up-features"
	_ "github.com/devsy-org/devsy/e2e/tests/upgrade"
)

func TestRunE2ETests(t *testing.T) {
	if runtime.GOOS != "linux" {
		go framework.ServeAgent()
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
