package agent

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type InjectTestSuite struct {
	suite.Suite
	ctx context.Context
}

func TestInjectTestSuite(t *testing.T) {
	suite.Run(t, new(InjectTestSuite))
}

func (s *InjectTestSuite) SetupTest() {
	s.ctx = context.Background()
}

func (s *InjectTestSuite) TestLocalInjection() {
	opts := &InjectOptions{
		Exec:    (&MockExecFunc{}).Exec,
		IsLocal: true,
		Command: "echo hello",
	}

	err := opts.Validate()
	s.NoError(err, "Validation of local injection options should succeed")
}

func (s *InjectTestSuite) TestOptionsDefaults() {
	opts := &InjectOptions{}
	opts.ApplyDefaults()

	s.NotZero(opts.Timeout, "Timeout should be set by defaults")
	s.NotEmpty(opts.DownloadURL, "DownloadURL should be set by defaults")
	s.NotEmpty(opts.LocalVersion, "LocalVersion should be set by defaults")
	s.Equal(opts.LocalVersion, opts.RemoteVersion, "RemoteVersion should default to LocalVersion")
}

func (s *InjectTestSuite) TestVersionChecker() {
	s.Run("Matches", func() {
		vc := &versionChecker{
			remoteVersion: "v1.0.0",
			skipCheck:     false,
		}
		mockExec := &MockExecFunc{Output: "v1.0.0\n"}

		detected, err := vc.detectRemoteAgentVersion(s.ctx, mockExec.Exec, "/path")
		s.NoError(err)
		s.Equal("v1.0.0", detected)
	})

	s.Run("Skip", func() {
		vc := &versionChecker{
			remoteVersion: "v1.0.0",
			skipCheck:     true,
		}
		mockExec := &MockExecFunc{Output: "v0.9.0\n"}

		detected, err := vc.detectRemoteAgentVersion(s.ctx, mockExec.Exec, "/path")
		s.NoError(err)
		s.Equal("v0.9.0", detected)
	})
}

func (s *InjectTestSuite) TestVersionChecker_BoundedByVersionCheckTimeout() {
	original := versionCheckTimeout
	versionCheckTimeout = 50 * time.Millisecond
	defer func() { versionCheckTimeout = original }()

	vc := &versionChecker{remoteVersion: "v2.0.0"}
	hangingExec := func(
		ctx context.Context,
		_ string,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) error {
		<-ctx.Done()
		return ctx.Err()
	}

	start := time.Now()
	detected, err := vc.detectRemoteAgentVersion(context.Background(), hangingExec, "/path")
	elapsed := time.Since(start)

	s.Empty(detected)
	s.Error(err)
	s.Less(
		elapsed, 2*time.Second,
		"detectRemoteAgentVersion took %s, want bounded by versionCheckTimeout", elapsed,
	)
	s.ErrorContains(err, "timed out")
}

// MockExecFunc is a helper for testing.
type MockExecFunc struct {
	CapturedCmd string
	Output      string
	Err         error
}

func (m *MockExecFunc) Exec(
	ctx context.Context,
	cmd string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	m.CapturedCmd = cmd
	if stdout != nil {
		_, _ = stdout.Write([]byte(m.Output))
	}
	return m.Err
}
