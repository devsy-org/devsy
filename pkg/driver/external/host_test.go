package external

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	fixtureKey       = "RUNTIME"
	fixtureImage     = "fixture"
	fixtureWorkspace = "workspace"
	delayedMode      = "delayed"
	workspaceCanary  = "workspace-canary-value"
)

type HostSuite struct {
	suite.Suite
	binary   string
	checksum string
}

func TestHostSuite(t *testing.T) { suite.Run(t, new(HostSuite)) }

func (s *HostSuite) SetupSuite() {
	s.binary = filepath.Join(s.T().TempDir(), "runtime space é")
	if runtime.GOOS == "windows" {
		s.binary += ".exe"
	}
	// #nosec G204 -- Builds a checked-in fixture into a private test directory.
	command := exec.Command("go", "build", "-o", s.binary, "./internal/testfixture")
	output, err := command.CombinedOutput()
	s.Require().NoError(err, string(output))
	// #nosec G304 -- Executable is built in this test's private temporary directory.
	data, err := os.ReadFile(s.binary)
	s.Require().NoError(err)
	sum := sha256.Sum256(data)
	s.checksum = hex.EncodeToString(sum[:])
}

func (s *HostSuite) TestLifecycleAcrossFreshProcesses() {
	host := s.host(fake.Normal)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.Require().NoError(host.Preflight(ctx, driver.PreflightOptions{DisableAutoStart: true}))
	s.Require().NoError(host.ProvisioningPreflight(ctx))
	found, err := host.FindDevContainer(ctx, fixtureWorkspace)
	s.Require().NoError(err)
	s.Nil(found)
	s.Require().
		NoError(host.RunImage(ctx, &runtimev1.RunImageRequest{WorkspaceId: fixtureWorkspace, Image: fixtureImage}))
	found, err = host.FindDevContainer(ctx, fixtureWorkspace)
	s.Require().NoError(err)
	s.Equal(config.ContainerStatusRunning, found.State.Status)
	architecture, err := host.TargetArchitecture(ctx, fixtureWorkspace)
	s.Require().NoError(err)
	s.Contains([]string{"amd64", "arm64"}, architecture)
	s.Require().NoError(host.StopDevContainer(ctx, fixtureWorkspace))
	found, err = host.FindDevContainer(ctx, fixtureWorkspace)
	s.Require().NoError(err)
	s.Equal(config.ContainerStatusExited, found.State.Status)
	s.Require().NoError(host.StartDevContainer(ctx, fixtureWorkspace))
	s.Require().NoError(host.DeleteDevContainer(ctx, fixtureWorkspace))
	s.Require().NoError(host.DeleteDevContainer(ctx, fixtureWorkspace))
}

func (s *HostSuite) TestNegotiationFailures() {
	for _, mode := range []string{
		fake.IncompatibleVersion, fake.MalformedInfo,
		fake.CrashBeforeHandshake, fake.CrashAfterHandshake,
	} {
		s.Run(mode, func() {
			_, err := s.newHost(context.Background(), mode, startupTimeout)
			s.Error(err)
		})
	}
}

func (s *HostSuite) TestCancellationDuringHandshake() {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := s.newHost(ctx, delayedMode, startupTimeout)
	s.ErrorIs(err, context.DeadlineExceeded)
	s.Less(time.Since(started), 5*time.Second)
}

func (s *HostSuite) TestStartupTimeout() {
	started := time.Now()
	_, err := s.newHost(context.Background(), delayedMode, 200*time.Millisecond)
	s.Error(err)
	s.Less(time.Since(started), 5*time.Second)
}

func (s *HostSuite) TestInfoNegotiationTimeout() {
	_, err := s.newHost(context.Background(), "block-info", 500*time.Millisecond)
	s.ErrorIs(err, context.DeadlineExceeded)
}

func (s *HostSuite) TestStructuredFailureAndCapabilitiesCopy() {
	host := s.host(fake.FailPreflight)
	err := host.Preflight(context.Background(), driver.PreflightOptions{})
	s.Equal(codes.Unavailable, status.Code(err))
	var runtimeError *RuntimeError
	s.True(errors.As(err, &runtimeError))
	s.Equal(runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNAVAILABLE, runtimeError.Category)
	info := host.Info()
	info.Capabilities.RecreateMode = runtimev1.RecreateMode_RECREATE_MODE_UNSPECIFIED
	s.NotEqual(info.Capabilities.RecreateMode, host.Info().Capabilities.RecreateMode)
}

func (s *HostSuite) TestRechecksExecutableBeforeEachOperation() {
	host := s.host(fake.Normal)
	host.config.Binaries[fixtureKey][0].Checksum = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	_, err := host.FindDevContainer(context.Background(), fixtureWorkspace)
	s.ErrorContains(err, "checksum")
}

func (s *HostSuite) TestRejectsInvalidIntentBeforeLaunch() {
	host := s.host(fake.Normal)
	s.Error(host.RunImage(context.Background(), nil))
	s.Error(host.RunImage(context.Background(), &runtimev1.RunImageRequest{
		WorkspaceId: fixtureWorkspace, Image: fixtureImage,
		Mounts: []*runtimev1.Mount{{Type: runtimev1.MountType_MOUNT_TYPE_UNSPECIFIED}},
	}))
	s.Error(host.StartDevContainer(context.Background(), ""))
	_, err := host.FindDevContainer(context.Background(), "")
	s.Error(err)
}

func (s *HostSuite) TestContainerConversionRejectsInvalidState() {
	for _, input := range []*runtimev1.ContainerDetails{
		nil, {Id: "id"}, {Id: "id", State: &runtimev1.ContainerState{Status: "unknown"}},
	} {
		_, err := convertContainer(input)
		s.Error(err)
	}
}

func (s *HostSuite) TestErrorRedactionPreservesStatus() {
	host := s.host(fake.Normal)
	host.redactor = secrets.NewRedactor([]string{"SECRET=private-canary-value"})
	err := host.operationError(
		context.Background(),
		"Start",
		status.Error(codes.PermissionDenied, "denied private-canary-value"),
	)
	s.Equal(codes.PermissionDenied, status.Code(err))
	s.NotContains(err.Error(), "private-canary-value")
}

func (s *HostSuite) TestCancellationReapsUncooperativeChild() {
	host := s.host("blocked")
	directory := host.config.External.Args[3]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- host.Preflight(ctx, driver.PreflightOptions{}) }()
	s.Require().Eventually(func() bool {
		_, err := os.Stat(filepath.Join(directory, "ready"))
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case err := <-result:
		s.ErrorIs(err, context.Canceled)
	case <-time.After(5 * time.Second):
		s.FailNow("runtime cancellation did not return")
	}
	s.assertReaped(directory)
}

func (s *HostSuite) TestInheritedEnvironmentAndTransportPrecedence() {
	s.T().Setenv("FIXTURE_SETTING", "inherited")
	s.T().Setenv("FIXTURE_EMPTY", "")
	s.T().Setenv("FIXTURE_TOKEN", "private-canary-value")
	s.T().Setenv("DEVSY_RUNTIME_PLUGIN", "provider-override")
	host := s.host("environment")
	s.Equal("inherited", host.Info().RuntimeVersion)
}

func (s *HostSuite) TestSplitDiagnosticRedaction() {
	var output bytes.Buffer
	writer := &diagnosticWriter{
		writer: &output,
		stream: secrets.NewStreamingRedactor(
			secrets.NewRedactor([]string{"TOKEN=private-canary-value"}),
		),
	}
	_, err := writer.Write([]byte("diagnostic private-canary-"))
	s.Require().NoError(err)
	_, err = writer.Write([]byte("value tail\n"))
	s.Require().NoError(err)
	writer.close()
	s.NotContains(output.String(), "private-canary-value")
	s.Contains(output.String(), "diagnostic")
	s.Contains(output.String(), "tail")
}

func (s *HostSuite) TestWorkspaceEnvironmentRedactedFromRuntimeErrors() {
	host := s.host("env-error")
	observed := log.InitTestObserved(s.T(), zapcore.DebugLevel)
	err := host.RunImage(context.Background(), &runtimev1.RunImageRequest{
		WorkspaceId: fixtureWorkspace,
		Image:       "fixture",
		Environment: map[string]string{"CUSTOM_VALUE": workspaceCanary},
	})
	s.Require().Error(err)
	s.NotContains(err.Error(), workspaceCanary)
	s.Equal(codes.Unavailable, status.Code(err))
	var runtimeError *RuntimeError
	s.True(errors.As(err, &runtimeError))
	s.True(runtimeError.Retryable)
	for _, entry := range observed.All() {
		s.NotContains(entry.Message, workspaceCanary)
	}
}

func (s *HostSuite) TestRealPathContainment() {
	root := s.T().TempDir()
	outside := filepath.Join(s.T().TempDir(), "outside")
	s.Require().NoError(os.WriteFile(outside, nil, 0o600))
	inside := filepath.Join(root, "inside")
	s.Require().NoError(os.WriteFile(inside, nil, 0o600))
	s.NoError(containRealPath(root, inside))
	link := filepath.Join(root, "escape")
	err := os.Symlink(outside, link)
	if err != nil && runtime.GOOS == "windows" {
		s.T().Skip("symlink creation unavailable on this Windows host")
	}
	s.Require().NoError(err)
	s.ErrorContains(containRealPath(root, link), "symlink escapes")
}

func (s *HostSuite) TestMissingWorkspace() {
	_, err := New(context.Background(), nil)
	s.ErrorContains(err, "workspace is missing")
}

func (s *HostSuite) assertReaped(directory string) {
	for _, kind := range []string{"supervisor", "plugin", "child"} {
		// #nosec G304 -- Fixture PID records live in a private test-owned temporary directory.
		data, err := os.ReadFile(filepath.Join(directory, kind+"-pids"))
		if os.IsNotExist(err) {
			continue
		}
		s.Require().NoError(err)
		for value := range strings.FieldsSeq(string(data)) {
			pid, err := strconv.ParseInt(value, 10, 32)
			s.Require().NoError(err)
			s.Eventually(func() bool {
				// #nosec G115 -- ParseInt above explicitly limits the value to 32 bits.
				exists, err := process.PidExists(int32(pid))
				return err == nil && !exists
			}, 5*time.Second, 10*time.Millisecond, "fixture %s PID %d survived session cleanup", kind, pid)
		}
	}
}

func (s *HostSuite) host(mode string) *Host {
	host, err := s.newHost(context.Background(), mode, startupTimeout)
	s.Require().NoError(err)
	return host
}

func (s *HostSuite) newHost(
	ctx context.Context,
	mode string,
	timeout time.Duration,
) (*Host, error) {
	directory := s.T().TempDir()
	s.T().Cleanup(func() { s.assertReaped(directory) })
	args := []string{"--mode", mode, "--state-dir", directory}
	if mode == delayedMode {
		args = append(args, "--delay", "1m")
	}
	agent := provider.ProviderAgentConfig{
		Driver:   provider.ExternalDriver,
		External: provider.ProviderExternalDriverConfig{Binary: fixtureKey, Args: args},
		Binaries: map[string][]*provider.ProviderBinary{
			fixtureKey: {
				{OS: runtime.GOOS, Arch: runtime.GOARCH, Path: s.binary, Checksum: s.checksum},
			},
		},
	}
	return newHost(ctx, hostOptions{
		config: agent, directory: s.T().TempDir(),
		supervisorBinary: s.binary, supervisorArgs: []string{"supervise", directory},
		environment: os.Environ(), timeout: timeout,
	})
}
