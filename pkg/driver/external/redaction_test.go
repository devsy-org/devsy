package external

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy/pkg/driver"
)

const workspaceSecretKey = "CUSTOM_VALUE"

func (s *HostSuite) TestExecRepeatedSecretAtStreamEnd() {
	s.T().Setenv("FIXTURE_TOKEN", "abcabc")
	host := s.host("stream-repeated-secret")
	var stdout, stderr bytes.Buffer
	s.Require().NoError(host.CommandDevContainer(context.Background(), &driver.CommandParams{
		WorkspaceID: fixtureWorkspace, Command: fixtureImage, Stdout: &stdout, Stderr: &stderr,
	}))
	s.Equal("***", stdout.String())
	s.Equal("***", stderr.String())
}

func (s *HostSuite) TestExecMasksWorkspaceEnvironmentAcrossFreshProcesses() {
	host := s.host("stream-workspace-secret")
	request := &runtimev1.RunImageRequest{
		WorkspaceId: fixtureWorkspace, Image: fixtureImage,
		Environment: map[string]string{workspaceSecretKey: workspaceCanary},
	}
	s.Require().NoError(host.RunImage(context.Background(), request))
	// Caller mutation cannot discard the value already installed in the runtime.
	request.Environment[workspaceSecretKey] = "later-caller-value"
	for _, raw := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		s.Require().NoError(host.CommandDevContainer(context.Background(), &driver.CommandParams{
			WorkspaceID: fixtureWorkspace, Command: fixtureImage,
			Stdout: &stdout, Stderr: &stderr, RawStdout: raw,
		}))
		if raw {
			s.Equal(workspaceCanary+"safe-tail", stdout.String())
		} else {
			s.Equal("***safe-tail", stdout.String())
		}
		s.Equal("***safe-tail", stderr.String())
	}
}

func (s *HostSuite) TestWorkspaceRedactionRetainsFailedRequestsAndPriorValues() {
	host := s.host("env-error")
	for _, value := range []string{"first-private-value", "rotated-private-value", "first-private-value"} {
		s.Error(host.RunImage(context.Background(), &runtimev1.RunImageRequest{
			WorkspaceId: fixtureWorkspace, Image: fixtureImage,
			Environment: map[string]string{workspaceSecretKey: value},
		}))
	}
	operation := host.forWorkspace(fixtureWorkspace, nil)
	s.Equal("*** ***", operation.redactor.Redact("first-private-value rotated-private-value"))
	s.Len(host.redactions.values[fixtureWorkspace], 2)
	s.Equal(
		"first-private-value",
		host.forWorkspace("other", nil).redactor.Redact("first-private-value"),
	)
	s.Equal("first-private-value", host.redactor.Redact("first-private-value"))
}

func (s *HostSuite) TestConcurrentWorkspaceRedactionSnapshots() {
	host := s.host(fake.Normal)
	const count = 20
	var workers sync.WaitGroup
	for index := range count {
		workers.Go(func() {
			value := fmt.Sprintf("concurrent-private-%d-value", index)
			host.forWorkspace(fixtureWorkspace, map[string]string{"VALUE": value})
			_ = host.forWorkspace(fixtureWorkspace, nil).redactor.Redact(value)
		})
	}
	workers.Wait()
	snapshot := host.forWorkspace(fixtureWorkspace, nil)
	for index := range count {
		s.Equal("***", snapshot.redactor.Redact(fmt.Sprintf("concurrent-private-%d-value", index)))
	}
}
