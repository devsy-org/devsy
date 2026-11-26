package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/status"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/suite"
)

type liveStatusSuite struct{ suite.Suite }

func TestLiveStatusSuite(t *testing.T) { suite.Run(t, new(liveStatusSuite)) }

type failedLiveRuntime struct{ workspace2.ContainerRuntime }

func (failedLiveRuntime) Exec(context.Context, workspace2.ExecRequest) (int, error) {
	return 7, &docker.LiveOutputError{Err: errors.New("12 bytes dropped")}
}

type blockingFinalReporter struct {
	calls   int
	release chan struct{}
}

func (r *blockingFinalReporter) Report(status.Event) {
	r.calls++
	if r.calls > 1 {
		<-r.release
	}
}

func (s *liveStatusSuite) TestOutputErrorDoesNotBlockFinalStatus() {
	reporter := &blockingFinalReporter{release: make(chan struct{})}
	defer close(reporter.release)
	command := &ExecCmd{
		GlobalFlags:    &flags.GlobalFlags{ResultFormat: formatPlain},
		statusReporter: reporter,
	}
	done := make(chan error, 1)
	go func() {
		opts := execOpts{runtime: failedLiveRuntime{}}
		done <- command.execAndReport(context.Background(), opts, nil, devcconfig.ResultEnvelope{})
	}()
	select {
	case err := <-done:
		s.Require().ErrorContains(err, "12 bytes dropped")
		s.Equal(1, reporter.calls)
	case <-time.After(time.Second):
		s.FailNow("final status did not return")
	}
}
