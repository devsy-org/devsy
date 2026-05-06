package ssh

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/devsy-org/devsy/e2e/framework"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("devsy portsAttributes e2e",
	ginkgo.Label("ssh", "ports-attributes"), func() {
		var initialDir string

		ginkgo.BeforeEach(func() {
			var err error
			initialDir, err = os.Getwd()
			framework.ExpectNoError(err)
		})

		ginkgo.It(
			"should forward port with onAutoForward=silent and skip port with onAutoForward=ignore",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				if runtime.GOOS == "windows" {
					ginkgo.Skip("skipping on windows")
				}

				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/ports-attributes")
				framework.ExpectNoError(err)

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_ = f.DevsyProviderAdd(ctx, "docker")
				err = f.DevsyProviderUse(ctx, "docker")
				framework.ExpectNoError(err)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				err = f.DevsyUp(ctx, tempDir)
				framework.ExpectNoError(err)

				allowedPort := 9500
				ignoredPort := 9501

				serverCtx, serverCancel := context.WithCancel(ctx)
				defer serverCancel()

				workspaceName := filepath.Base(tempDir)

				// Start server on the allowed port (9500, label=TestService, protocol=http)
				// #nosec G204 -- test command with controlled arguments
				serverCmd := exec.CommandContext(serverCtx, f.DevsyBinDir+"/"+f.DevsyBinName,
					"ssh", tempDir, "--command",
					"go run /workspaces/"+workspaceName+"/server.go "+strconv.Itoa(allowedPort),
				)
				err = serverCmd.Start()
				framework.ExpectNoError(err)

				// Start server on the ignored port (9501)
				// #nosec G204 -- test command with controlled arguments
				ignoredCmd := exec.CommandContext(serverCtx, f.DevsyBinDir+"/"+f.DevsyBinName,
					"ssh", tempDir, "--command",
					"go run /workspaces/"+workspaceName+"/server.go "+strconv.Itoa(ignoredPort),
				)
				err = ignoredCmd.Start()
				framework.ExpectNoError(err)

				var wg sync.WaitGroup
				wg.Go(func() { _ = serverCmd.Wait() })
				wg.Go(func() { _ = ignoredCmd.Wait() })

				// Forward port 9500 (should succeed per portsAttributes)
				portForwardCtx, cancelPort := context.WithTimeout(ctx, 60*time.Second)
				defer cancelPort()
				wg.Go(func() {
					_ = f.DevsyPortTest(portForwardCtx, strconv.Itoa(allowedPort), tempDir)
				})

				ginkgo.DeferCleanup(func() {
					serverCancel()
					cancelPort()
					wg.Wait()
				})

				// Port 9500 should be reachable (onAutoForward=silent, label=TestService)
				address := net.JoinHostPort("localhost", strconv.Itoa(allowedPort))
				gomega.Eventually(func() string {
					conn, err := net.DialTimeout("tcp", address, 3*time.Second)
					if err == nil {
						_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
						buf := make([]byte, 1024)
						n, readErr := conn.Read(buf)
						_ = conn.Close()
						if readErr == nil && n > 0 {
							return string(buf[:n])
						}
					}
					return ""
				}, 60*time.Second, 2*time.Second).Should(
					gomega.Equal("PONG\n"),
					"Port 9500 (onAutoForward=silent, label=TestService) should be forwarded",
				)

				// Port 9501 should NOT be reachable locally (onAutoForward=ignore)
				ignoredAddr := net.JoinHostPort("localhost", strconv.Itoa(ignoredPort))
				gomega.Consistently(func() bool {
					conn, err := net.DialTimeout("tcp", ignoredAddr, 1*time.Second)
					if err != nil {
						return false
					}
					_ = conn.Close()
					return true
				}, 5*time.Second, 1*time.Second).Should(
					gomega.BeFalse(),
					"Port 9501 (onAutoForward=ignore) should NOT be forwarded",
				)
			},
		)

		ginkgo.It(
			"should forward port with notify policy and apply label metadata",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				if runtime.GOOS == "windows" {
					ginkgo.Skip("skipping on windows")
				}

				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/ports-attributes")
				framework.ExpectNoError(err)

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_ = f.DevsyProviderAdd(ctx, "docker")
				err = f.DevsyProviderUse(ctx, "docker")
				framework.ExpectNoError(err)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				err = f.DevsyUp(ctx, tempDir)
				framework.ExpectNoError(err)

				// Port 9502 has otherPortsAttributes with onAutoForward=notify (should forward)
				notifyPort := 9502

				serverCtx, serverCancel := context.WithCancel(ctx)
				defer serverCancel()

				workspaceName := filepath.Base(tempDir)

				// #nosec G204 -- test command with controlled arguments
				serverCmd := exec.CommandContext(serverCtx, f.DevsyBinDir+"/"+f.DevsyBinName,
					"ssh", tempDir, "--command",
					"go run /workspaces/"+workspaceName+"/server.go "+strconv.Itoa(notifyPort),
				)
				err = serverCmd.Start()
				framework.ExpectNoError(err)

				var wg sync.WaitGroup
				wg.Go(func() { _ = serverCmd.Wait() })

				portForwardCtx, cancelPort := context.WithTimeout(ctx, 60*time.Second)
				defer cancelPort()
				wg.Go(func() {
					_ = f.DevsyPortTest(portForwardCtx, strconv.Itoa(notifyPort), tempDir)
				})

				ginkgo.DeferCleanup(func() {
					serverCancel()
					cancelPort()
					wg.Wait()
				})

				// Port 9502 falls through to otherPortsAttributes (onAutoForward=notify)
				// and should be forwarded
				address := net.JoinHostPort("localhost", strconv.Itoa(notifyPort))
				gomega.Eventually(func() string {
					conn, err := net.DialTimeout("tcp", address, 3*time.Second)
					if err == nil {
						_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
						buf := make([]byte, 1024)
						n, readErr := conn.Read(buf)
						_ = conn.Close()
						if readErr == nil && n > 0 {
							return string(buf[:n])
						}
					}
					return ""
				}, 60*time.Second, 2*time.Second).Should(
					gomega.Equal("PONG\n"),
					"Port 9502 (otherPortsAttributes onAutoForward=notify) should be forwarded",
				)
			},
		)

		ginkgo.It(
			"should skip forwarding when requireLocalPort=true and host port is occupied",
			ginkgo.SpecTimeout(framework.TimeoutShort()),
			func(ctx context.Context) {
				if runtime.GOOS == "windows" {
					ginkgo.Skip("skipping on windows")
				}

				requirePort := 9503

				// Occupy the host port BEFORE starting the workspace
				hostListener, err := net.Listen(
					"tcp",
					net.JoinHostPort("localhost", strconv.Itoa(requirePort)),
				)
				framework.ExpectNoError(err)
				ginkgo.DeferCleanup(func() {
					_ = hostListener.Close()
				})

				tempDir, err := framework.CopyToTempDir("tests/ssh/testdata/ports-attributes")
				framework.ExpectNoError(err)

				f := framework.NewDefaultFramework(initialDir + "/bin")
				_ = f.DevsyProviderAdd(ctx, "docker")
				err = f.DevsyProviderUse(ctx, "docker")
				framework.ExpectNoError(err)

				ginkgo.DeferCleanup(func(cleanupCtx context.Context) {
					_ = f.DevsyWorkspaceDelete(cleanupCtx, tempDir)
					framework.CleanupTempDir(initialDir, tempDir)
				})

				err = f.DevsyUp(ctx, tempDir)
				framework.ExpectNoError(err)

				serverCtx, serverCancel := context.WithCancel(ctx)
				defer serverCancel()

				workspaceName := filepath.Base(tempDir)

				// Start server inside the container on port 9503
				// #nosec G204 -- test command with controlled arguments
				serverCmd := exec.CommandContext(serverCtx, f.DevsyBinDir+"/"+f.DevsyBinName,
					"ssh", tempDir, "--command",
					"go run /workspaces/"+workspaceName+"/server.go "+strconv.Itoa(requirePort),
				)
				err = serverCmd.Start()
				framework.ExpectNoError(err)

				var wg sync.WaitGroup
				wg.Go(func() { _ = serverCmd.Wait() })

				portForwardCtx, cancelPort := context.WithTimeout(ctx, 60*time.Second)
				defer cancelPort()
				wg.Go(func() {
					_ = f.DevsyPortTest(portForwardCtx, strconv.Itoa(requirePort), tempDir)
				})

				ginkgo.DeferCleanup(func() {
					serverCancel()
					cancelPort()
					wg.Wait()
				})

				// Port 9503 should NOT be reachable because the host port is occupied
				// and requireLocalPort=true prevents fallback to another port
				requireAddr := net.JoinHostPort("localhost", strconv.Itoa(requirePort))
				gomega.Consistently(func() bool {
					conn, dialErr := net.DialTimeout("tcp", requireAddr, 1*time.Second)
					if dialErr != nil {
						return false
					}
					// If we connect, check if it's the host listener (not the container server)
					_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
					buf := make([]byte, 1024)
					n, readErr := conn.Read(buf)
					_ = conn.Close()
					// The host listener doesn't write anything, so we should get
					// a timeout or EOF — not "PONG\n" from the container
					if readErr == nil && n > 0 && string(buf[:n]) == "PONG\n" {
						return true
					}
					return false
				}, 5*time.Second, 1*time.Second).Should(
					gomega.BeFalse(),
					"Port 9503 (requireLocalPort=true) should NOT be forwarded when host port is occupied",
				)
			},
		)
	})
