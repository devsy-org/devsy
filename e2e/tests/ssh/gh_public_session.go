package ssh

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/require"
)

func (p *publicGHFixture) testLiveServiceReuse(
	t ginkgo.GinkgoTInterface,
	ctx context.Context,
	workspace string,
) context.CancelFunc {
	t.Helper()
	keeper, cancel := context.WithCancel(ctx)
	//nolint:gosec // Execute the built test CLI against the isolated test workspace.
	cmd := exec.CommandContext(keeper, filepath.Join(p.f.DevsyBinDir, p.f.DevsyBinName),
		cmdWorkspace, cmdSSH, workspace, flagCommand, publicGHAPICommand+" && sleep 60")
	docker.PrepareForGroupCancellation(cmd)
	cmd.WaitDelay = 250 * time.Millisecond
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	var stderr publicSessionStderr
	cmd.Stderr = &stderr
	require.NoError(t, cmd.Start())
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	drained := make(chan struct{})
	stop := stopPublicSession(t, cancel, done, drained)
	t.Cleanup(stop)
	first := make(chan string, 1)
	go func() {
		defer close(drained)
		reader := bufio.NewReader(io.LimitReader(stdout, 4096))
		line, _ := reader.ReadString('\n')
		first <- strings.TrimSpace(line)
	}()
	select {
	case line := <-first:
		if line != publicGHUser {
			stop()
			require.Equal(t, publicGHUser, line,
				"first public command must authenticate without a setup wait: %s", stderr.String())
		}
	case <-time.After(20 * time.Second):
		stop()
		t.Fatalf("first public GitHub command did not complete: %s", stderr.String())
	}
	out, err := p.f.DevsySSHOnce(ctx, workspace, publicGHAPICommand)
	require.NoError(t, err, "second connection must reuse the still-live owner")
	require.Equal(t, publicGHUser, strings.TrimSpace(out))
	return stop
}

func stopPublicSession(
	t ginkgo.GinkgoTInterface,
	cancel context.CancelFunc,
	done, drained <-chan struct{},
) context.CancelFunc {
	t.Helper()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			select {
			case <-done:
			case <-cleanup.Done():
				t.Error("public SSH session did not exit during cleanup")
			}
			select {
			case <-drained:
			case <-cleanup.Done():
				t.Error("public SSH output reader did not exit during cleanup")
			}
		})
	}
}
