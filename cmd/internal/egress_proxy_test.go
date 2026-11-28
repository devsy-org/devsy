//go:build !windows

package cmdinternal

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/suite"
)

const egressConfigFlag = "--config"

type EgressProxyCmdSuite struct {
	suite.Suite
}

func TestEgressProxyCmdSuite(t *testing.T) {
	suite.Run(t, new(EgressProxyCmdSuite))
}

func (s *EgressProxyCmdSuite) TestTheCommandIsHiddenAndSkipsTheSharedPreRun() {
	cmd := NewEgressProxyCmd()
	s.True(cmd.Hidden)
	s.Equal("egress-proxy", cmd.Name())
	s.Require().NotNil(cmd.PersistentPreRunE)
	s.NoError(cmd.PersistentPreRunE(cmd, nil))
	found, _, err := NewInternalCmd(nil).Find([]string{"egress-proxy"})
	s.Require().NoError(err)
	s.Equal("egress-proxy", found.Name())
}

func (s *EgressProxyCmdSuite) TestTheConfigFlagIsRequiredAndArgsAreRefused() {
	cmd := NewEgressProxyCmd()
	cmd.SetArgs(nil)
	cmd.SetOut(&discard{})
	cmd.SetErr(&discard{})
	s.Error(cmd.Execute())

	cmd = NewEgressProxyCmd()
	cmd.SetArgs([]string{egressConfigFlag, "x.json", "extra"})
	cmd.SetOut(&discard{})
	cmd.SetErr(&discard{})
	s.Error(cmd.Execute())
}

func (s *EgressProxyCmdSuite) TestAWildcardBindFailsBeforeAnythingStarts() {
	dir := s.T().TempDir()
	raw, err := json.Marshal(map[string]any{
		"version":      1,
		"sessionId":    "p-test-pool-abcdefghij",
		"bind":         "0.0.0.0:3128",
		"eventsSocket": filepath.Join(dir, "events.sock"),
		"egress":       poolconfig.Egress{Default: poolconfig.EgressDeny},
		"alwaysDeny":   []string{"10.0.0.0/8"},
	})
	s.Require().NoError(err)
	path := filepath.Join(dir, "egress.json")
	s.Require().NoError(os.WriteFile(path, raw, 0o600))
	s.ErrorContains(runEgressProxy(context.Background(), path), "bind")
	s.Error(runEgressProxy(context.Background(), filepath.Join(dir, "missing.json")))
}

func (s *EgressProxyCmdSuite) TestTheSharedRootPreRunDoesNotRun() {
	var rootRan bool
	root := &cobra.Command{
		Use:               "devsy",
		PersistentPreRunE: func(*cobra.Command, []string) error { rootRan = true; return nil },
	}
	root.AddCommand(NewInternalCmd(&flags.GlobalFlags{}))
	root.SetArgs(
		[]string{
			"internal",
			"egress-proxy",
			egressConfigFlag,
			filepath.Join(s.T().TempDir(), "x.json"),
		},
	)
	root.SetOut(&discard{})
	root.SetErr(&discard{})
	s.Error(root.Execute())
	s.False(rootRan, "the root pre-run loads config the sidecar does not have")
}

func (s *EgressProxyCmdSuite) TestSIGTERMStopsTheProxyAndReturnsNil() {
	dir, err := os.MkdirTemp("", "esc")
	s.Require().NoError(err)
	s.T().Cleanup(func() { _ = os.RemoveAll(dir) })
	raw, err := json.Marshal(map[string]any{
		"version":      1,
		"sessionId":    "p-test-pool-abcdefghij",
		"bind":         "127.0.0.1:0",
		"eventsSocket": filepath.Join(dir, "absent.sock"),
		"egress":       poolconfig.Egress{Default: poolconfig.EgressDeny},
		"alwaysDeny":   []string{"10.0.0.0/8"},
	})
	s.Require().NoError(err)
	path := filepath.Join(dir, "egress.json")
	s.Require().NoError(os.WriteFile(path, raw, 0o600))

	done := make(chan error, 1)
	go func() { done <- runEgressProxy(context.Background(), path) }()
	select {
	case err := <-done:
		s.FailNow("proxy stopped before the signal", "%v", err)
	case <-time.After(500 * time.Millisecond):
	}
	s.Require().NoError(syscall.Kill(os.Getpid(), syscall.SIGTERM))
	select {
	case err := <-done:
		s.NoError(err)
	case <-time.After(5 * time.Second):
		s.FailNow("proxy did not stop on SIGTERM")
	}
}

type discard struct{}

func (*discard) Write(p []byte) (int, error) { return len(p), nil }
