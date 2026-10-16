package devcontainer

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"go.uber.org/zap/zapcore"
)

type failingArchitectureDriver struct {
	mockDriver
	err error
}

func (d *failingArchitectureDriver) TargetArchitecture(context.Context, string) (string, error) {
	return "", d.err
}

func TestAttemptPreStartDeliveryLogsPlannedPostStartPhase(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.DebugLevel)
	r := newTestRunner(&mockDriver{})

	// A nil configuration would fail if pre-start options were built. A
	// post-start strategy should be recognized before reaching that work.
	r.attemptPreStartDelivery(context.Background(), nil, nil, nil)

	if got := logs.FilterMessage("agent delivery scheduled for post-start").Len(); got != 1 {
		t.Fatalf("planned post-start diagnostic count = %d, want 1", got)
	}
	if got := logs.FilterMessage("strategy phase is post-start, not pre-start").Len(); got != 0 {
		t.Fatalf("phase-mismatch error diagnostic count = %d, want 0", got)
	}
}

func TestAttemptPreStartDeliveryLogsFailureAndContinues(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.DebugLevel)
	failure := errors.New("architecture unavailable")
	r := newTestRunner(&failingArchitectureDriver{err: failure})
	r.workspaceConfig.Agent.Driver = provider.DockerDriver

	r.attemptPreStartDelivery(
		context.Background(),
		&config.MergedDevContainerConfig{},
		&resolveParams{substitutionContext: &config.SubstitutionContext{}},
		&config.BuildInfo{ImageMetadata: &config.ImageMetadataConfig{}},
	)

	entries := logs.FilterMessage(
		"pre-start agent delivery failed; will use post-start: " +
			"resolve target architecture: architecture unavailable",
	).All()
	if len(entries) != 1 {
		t.Fatalf("pre-start delivery failure diagnostic count = %d, want 1", len(entries))
	}
	if len(logs.FilterMessage("agent delivery scheduled for post-start").All()) != 0 {
		t.Fatal("pre-start failure was misreported as a planned post-start strategy")
	}
}
