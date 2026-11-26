package status

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type liveStatusSuite struct{ suite.Suite }

func TestLiveStatusSuite(t *testing.T) { suite.Run(t, new(liveStatusSuite)) }

type undeliverableOutput struct{}

func (undeliverableOutput) Error() string         { return "12 bytes dropped" }
func (undeliverableOutput) SkipFinalStatus() bool { return true }

type blockedFinalReporter struct {
	calls   int
	release chan struct{}
}

func (r *blockedFinalReporter) Report(e Event) {
	r.calls++
	if e.State != StateStarted {
		<-r.release
	}
}

func (s *liveStatusSuite) TestNestedOperationsSkipFinalStatus() {
	reporter := &blockedFinalReporter{release: make(chan struct{})}
	defer close(reporter.release)
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), reporter, Operation{}, func(ctx context.Context) error {
			return Run(ctx, reporter, Operation{}, func(context.Context) error {
				return fmt.Errorf("command: %w", undeliverableOutput{})
			})
		})
	}()
	select {
	case err := <-done:
		s.Require().ErrorContains(err, "12 bytes dropped")
		s.Equal(2, reporter.calls)
	case <-time.After(time.Second):
		s.FailNow("final status did not return")
	}
}
