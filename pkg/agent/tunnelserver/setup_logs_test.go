package tunnelserver

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAppendSetupLogsUnimplementedWithoutSink(t *testing.T) {
	_, err := New().AppendSetupLogs(context.Background(), &tunnel.AppendSetupLogsRequest{})
	require.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestAppendSetupLogsValidatesExpectedRun(t *testing.T) {
	store, err := workspacelogs.Open(
		workspacelogs.Options{Root: filepath.Join(t.TempDir(), "logs")},
	)
	require.NoError(t, err)
	runID := workspacelogs.NewID()
	sink, err := workspacelogs.NewSink(workspacelogs.SinkOptions{
		Store: store,
		Spec:  workspacelogs.RunSpec{RunID: runID, Context: "default", Command: "up"},
	})
	require.NoError(t, err)
	srv := New(WithSetupLogSink(sink))

	resp, err := srv.AppendSetupLogs(context.Background(), &tunnel.AppendSetupLogsRequest{
		SchemaVersion: workspacelogs.SchemaVersion,
		RunId:         workspacelogs.NewID(),
		ProducerId:    workspacelogs.NewID(),
		FirstSequence: 1, LastSequence: 1,
		Records: [][]byte{[]byte(`{}`)},
	})
	require.NoError(t, err)
	require.Equal(t, tunnel.SetupLogResult_SETUP_LOG_RESULT_REJECTED_INVALID, resp.GetResult())

	huge := make([]byte, workspacelogs.MaxPageBytes)
	resp, err = srv.AppendSetupLogs(context.Background(), &tunnel.AppendSetupLogsRequest{
		SchemaVersion: workspacelogs.SchemaVersion,
		RunId:         runID, ProducerId: workspacelogs.NewID(),
		FirstSequence: 1, LastSequence: 1, Records: [][]byte{huge},
	})
	require.NoError(t, err)
	require.Equal(t, tunnel.SetupLogResult_SETUP_LOG_RESULT_REJECTED_INVALID, resp.GetResult())
}
