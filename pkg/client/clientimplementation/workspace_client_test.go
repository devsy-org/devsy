package clientimplementation

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleForceError(t *testing.T) {
	sentinel := errors.New("boom")

	tests := []struct {
		name    string
		err     error
		force   bool
		wantErr error
	}{
		{name: "no error", err: nil, force: false, wantErr: nil},
		{name: "no error forced", err: nil, force: true, wantErr: nil},
		{name: "error not forced", err: sentinel, force: false, wantErr: sentinel},
		{name: "error forced", err: sentinel, force: true, wantErr: nil},
		{
			name:    "deadline forced",
			err:     context.DeadlineExceeded,
			force:   true,
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantErr, handleForceError(tt.err, tt.force))
		})
	}
}

func TestDeleteContext(t *testing.T) {
	s := &workspaceClient{}

	t.Run("empty grace period has no deadline", func(t *testing.T) {
		ctx, cancel := s.deleteContext(context.Background(), "")
		defer cancel()
		_, ok := ctx.Deadline()
		assert.False(t, ok)
	})

	t.Run("invalid grace period has no deadline", func(t *testing.T) {
		ctx, cancel := s.deleteContext(context.Background(), "not-a-duration")
		defer cancel()
		_, ok := ctx.Deadline()
		assert.False(t, ok)
	})

	t.Run("valid grace period sets deadline", func(t *testing.T) {
		ctx, cancel := s.deleteContext(context.Background(), "50ms")
		defer cancel()
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		assert.WithinDuration(t, time.Now().Add(50*time.Millisecond), deadline, time.Second)
	})
}

func TestRemoveAll(t *testing.T) {
	t.Run("missing path returns nil", func(t *testing.T) {
		assert.NoError(t, removeAll(filepath.Join(t.TempDir(), "does-not-exist")))
	})

	t.Run("existing tree is removed", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "tree")
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "nested"), 0o750))

		require.NoError(t, removeAll(dir))
		_, err := os.Stat(dir)
		assert.True(t, os.IsNotExist(err))
	})
}

func TestRunCommand(t *testing.T) {
	t.Run("empty command is a no-op", func(t *testing.T) {
		assert.NoError(t, RunCommand(context.Background(), RunCommandOptions{}))
	})

	t.Run("multi-arg command runs via exec", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		err := RunCommand(context.Background(),
			RunCommandOptions{
				Command: types.StrArray{"echo", "hello"},
				Stdout:  stdout,
			})
		require.NoError(t, err)
		assert.Equal(t, "hello", strings.TrimSpace(stdout.String()))
	})

	t.Run("single command runs via emulated shell", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		err := RunCommand(context.Background(), RunCommandOptions{
			Command: types.StrArray{"echo emulated"},
			Stdout:  stdout,
		})
		require.NoError(t, err)
		assert.Equal(t, "emulated", strings.TrimSpace(stdout.String()))
	})
}

func TestLogBusy(t *testing.T) {
	t.Run("within threshold keeps timestamp", func(t *testing.T) {
		start := time.Now()
		logBusy(&start)
		assert.WithinDuration(t, time.Now(), start, time.Second)
	})

	t.Run("past threshold resets timestamp", func(t *testing.T) {
		start := time.Now().Add(-2 * logThreshold)
		logBusy(&start)
		assert.WithinDuration(t, time.Now(), start, time.Second)
	})
}

func TestStartWaitStatusHandlers(t *testing.T) {
	ctx := context.Background()

	t.Run("stopped without create errors", func(t *testing.T) {
		err := handleStoppedStatus(ctx, &fakeWorkspaceClient{}, false)
		assert.EqualError(t, err, "workspace is stopped")
	})

	t.Run("stopped with create starts workspace", func(t *testing.T) {
		fake := &fakeWorkspaceClient{}
		require.NoError(t, handleStoppedStatus(ctx, fake, true))
		assert.True(t, fake.startCalled)
	})

	t.Run("stopped start failure is wrapped", func(t *testing.T) {
		fake := &fakeWorkspaceClient{startErr: errors.New("nope")}
		err := handleStoppedStatus(ctx, fake, true)
		assert.ErrorContains(t, err, "start workspace")
	})

	t.Run("not found without create errors", func(t *testing.T) {
		err := handleNotFoundStatus(ctx, &fakeWorkspaceClient{}, false)
		assert.EqualError(t, err, "workspace not found")
	})

	t.Run("not found with create creates workspace", func(t *testing.T) {
		fake := &fakeWorkspaceClient{}
		require.NoError(t, handleNotFoundStatus(ctx, fake, true))
		assert.True(t, fake.createCalled)
	})
}

func TestStartWaitReturnsWhenRunning(t *testing.T) {
	fake := &fakeWorkspaceClient{status: client.StatusRunning}
	require.NoError(t, StartWait(context.Background(), fake, false))
}

func TestStartWaitPropagatesStatusError(t *testing.T) {
	fake := &fakeWorkspaceClient{statusErr: errors.New("status failed")}
	assert.ErrorContains(t, StartWait(context.Background(), fake, false), "status failed")
}

type fakeWorkspaceClient struct {
	client.WorkspaceClient

	status    client.Status
	statusErr error

	startCalled  bool
	startErr     error
	createCalled bool
	createErr    error
}

func (f *fakeWorkspaceClient) Status(
	context.Context,
	client.StatusOptions,
) (client.Status, error) {
	return f.status, f.statusErr
}

func (f *fakeWorkspaceClient) Start(context.Context) error {
	f.startCalled = true
	return f.startErr
}

func (f *fakeWorkspaceClient) Create(context.Context) error {
	f.createCalled = true
	return f.createErr
}
