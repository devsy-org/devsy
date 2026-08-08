package tunnelserver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type fakeSendResultClient struct {
	tunnel.TunnelClient
	sent           *tunnel.Message
	ctxErrAtCall   error
	ctxHadDeadline bool
}

func (f *fakeSendResultClient) SendResult(
	ctx context.Context,
	in *tunnel.Message,
	_ ...grpc.CallOption,
) (*tunnel.Empty, error) {
	f.sent = in
	f.ctxErrAtCall = ctx.Err()
	_, f.ctxHadDeadline = ctx.Deadline()
	return &tunnel.Empty{}, nil
}

func decodeSentResult(t *testing.T, client *fakeSendResultClient) *config.Result {
	t.Helper()
	require.NotNil(t, client.sent)
	result := &config.Result{}
	require.NoError(t, json.Unmarshal([]byte(client.sent.Message), result))
	return result
}

func TestReportResult_NilResultSuccess(t *testing.T) {
	client := &fakeSendResultClient{}

	result, err := ReportResult(
		context.Background(),
		client,
		func(context.Context) (*config.Result, error) { return nil, nil },
	)

	require.NoError(t, err)
	require.Nil(t, result)
	require.Empty(t, decodeSentResult(t, client).Error)
}

func TestReportResult_NilResultFailure_SynthesizesError(t *testing.T) {
	client := &fakeSendResultClient{}
	jobErr := errors.New("build: exit status 1")

	result, err := ReportResult(
		context.Background(),
		client,
		func(context.Context) (*config.Result, error) { return nil, jobErr },
	)

	require.ErrorIs(t, err, jobErr)
	require.Nil(t, result)
	require.Equal(t, jobErr.Error(), decodeSentResult(t, client).Error)
}

func TestReportResult_RichResultFailure_PreservesFields(t *testing.T) {
	client := &fakeSendResultClient{}
	jobErr := errors.New("devcontainer up failed")
	richResult := &config.Result{Error: jobErr.Error(), RecoveryAvailable: true}

	result, err := ReportResult(
		context.Background(),
		client,
		func(context.Context) (*config.Result, error) { return richResult, jobErr },
	)

	require.ErrorIs(t, err, jobErr)
	require.Same(t, richResult, result)
	sent := decodeSentResult(t, client)
	require.Equal(t, jobErr.Error(), sent.Error)
	require.True(t, sent.RecoveryAvailable)
}

func TestReportResult_UsesContextIndependentOfCaller(t *testing.T) {
	client := &fakeSendResultClient{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ReportResult(
		ctx,
		client,
		func(context.Context) (*config.Result, error) { return nil, nil },
	)

	require.NoError(t, err)
	require.NoError(t, client.ctxErrAtCall)
	require.True(
		t,
		client.ctxHadDeadline,
		"expected SendResult to be called with a bounded context",
	)
}

func TestReportResult_SendFailure_SuccessfulJobReportsSendErr(t *testing.T) {
	client := &erroringSendResultClient{err: errors.New("transport down")}

	_, err := ReportResult(
		context.Background(),
		client,
		func(context.Context) (*config.Result, error) { return nil, nil },
	)

	require.ErrorIs(t, err, client.err)
}

func TestReportResult_SendFailure_JobErrorTakesPrecedence(t *testing.T) {
	client := &erroringSendResultClient{err: errors.New("transport down")}
	jobErr := errors.New("build: exit status 1")

	_, err := ReportResult(
		context.Background(),
		client,
		func(context.Context) (*config.Result, error) { return nil, jobErr },
	)

	require.ErrorIs(t, err, jobErr)
}

func TestReportResult_PanicInFnIsReported(t *testing.T) {
	client := &fakeSendResultClient{}

	result, err := ReportResult(
		context.Background(),
		client,
		func(context.Context) (*config.Result, error) {
			panic("boom")
		},
	)

	require.Nil(t, result)
	require.ErrorContains(t, err, "boom")
	require.Contains(t, decodeSentResult(t, client).Error, "boom")
}

func TestReportResult_DoesNotMutateCallersResult(t *testing.T) {
	client := &fakeSendResultClient{}
	jobErr := errors.New("devcontainer up failed")
	callerResult := &config.Result{}

	_, err := ReportResult(
		context.Background(),
		client,
		func(context.Context) (*config.Result, error) { return callerResult, jobErr },
	)

	require.ErrorIs(t, err, jobErr)
	require.Empty(
		t,
		callerResult.Error,
		"ReportResult must not mutate the caller-owned result struct",
	)
	require.Equal(t, jobErr.Error(), decodeSentResult(t, client).Error)
}

type erroringSendResultClient struct {
	tunnel.TunnelClient
	err error
}

func (f *erroringSendResultClient) SendResult(
	context.Context,
	*tunnel.Message,
	...grpc.CallOption,
) (*tunnel.Empty, error) {
	return nil, f.err
}
