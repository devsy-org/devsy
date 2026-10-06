package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type execStream = grpc.BidiStreamingServer[runtimev1.ExecClientMessage, runtimev1.ExecServerMessage]

func (f *fixture) Exec(stream execStream) error {
	if !strings.HasPrefix(f.mode, "stream-") {
		return f.Driver.Exec(stream)
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if f.mode == "stream-block-child" {
		_, err := f.Preflight(stream.Context(), &runtimev1.PreflightRequest{})
		return err
	}
	frames, err := f.fixtureFrames(first.GetStart())
	if err != nil {
		return err
	}
	for _, frame := range frames {
		if err := stream.Send(frame); err != nil {
			return err
		}
	}
	if f.mode == "stream-exit-rpc-error" {
		return status.Error(codes.Unavailable, "failure after exit")
	}
	return nil
}

func (f *fixture) fixtureFrames(
	start *runtimev1.ExecStart,
) ([]*runtimev1.ExecServerMessage, error) {
	exit := &runtimev1.ExecServerMessage{
		Payload: &runtimev1.ExecServerMessage_Exit{Exit: &runtimev1.ExecExit{}},
	}
	switch f.mode {
	case "stream-secret":
		return secretFrames(exit), nil
	case "stream-repeated-secret":
		return repeatedSecretFrames(exit), nil
	case "stream-workspace-secret":
		return f.workspaceSecretFrames(start.WorkspaceId, exit)
	case "stream-metadata":
		data, err := json.Marshal(start)
		return []*runtimev1.ExecServerMessage{stdoutFrame(data), exit}, err
	default:
		return malformedFrames(f.mode, exit), nil
	}
}

func malformedFrames(
	mode string,
	exit *runtimev1.ExecServerMessage,
) []*runtimev1.ExecServerMessage {
	switch mode {
	case "stream-missing-exit":
		return nil
	case "stream-duplicate-exit":
		return []*runtimev1.ExecServerMessage{exit, exit}
	case "stream-after-exit":
		return []*runtimev1.ExecServerMessage{exit, stdoutFrame([]byte("late"))}
	case "stream-unset-frame":
		return []*runtimev1.ExecServerMessage{{}, exit}
	case "stream-empty-chunk":
		return []*runtimev1.ExecServerMessage{stdoutFrame(nil), exit}
	case "stream-large-chunk":
		return []*runtimev1.ExecServerMessage{
			stdoutFrame(make([]byte, runtimev1.ChunkSize+1)),
			exit,
		}
	default:
		return []*runtimev1.ExecServerMessage{exit}
	}
}

func stdoutFrame(data []byte) *runtimev1.ExecServerMessage {
	return &runtimev1.ExecServerMessage{
		Payload: &runtimev1.ExecServerMessage_Stdout{Stdout: &runtimev1.OutputChunk{Data: data}},
	}
}

func secretFrames(exit *runtimev1.ExecServerMessage) []*runtimev1.ExecServerMessage {
	return splitSecretFrames("private-canary-value", exit)
}

func splitSecretFrames(
	secret string,
	exit *runtimev1.ExecServerMessage,
) []*runtimev1.ExecServerMessage {
	var frames []*runtimev1.ExecServerMessage
	for _, chunk := range []string{secret[:len(secret)/2], secret[len(secret)/2:], "safe-tail"} {
		frames = append(frames, stdoutFrame([]byte(chunk)), &runtimev1.ExecServerMessage{
			Payload: &runtimev1.ExecServerMessage_Stderr{
				Stderr: &runtimev1.OutputChunk{Data: []byte(chunk)},
			},
		})
	}
	return append(frames, exit)
}

func repeatedSecretFrames(exit *runtimev1.ExecServerMessage) []*runtimev1.ExecServerMessage {
	return []*runtimev1.ExecServerMessage{
		stdoutFrame([]byte("abcabc")),
		{
			Payload: &runtimev1.ExecServerMessage_Stderr{
				Stderr: &runtimev1.OutputChunk{Data: []byte("abcabc")},
			},
		},
		exit,
	}
}

func (f *fixture) environmentPath(workspaceID string) string {
	return filepath.Join(
		f.directory,
		fmt.Sprintf("%x-environment", sha256.Sum256([]byte(workspaceID))),
	)
}

func (f *fixture) saveEnvironment(request *runtimev1.RunImageRequest) error {
	data, err := json.Marshal(request.Environment)
	if err != nil {
		return err
	}
	return os.WriteFile(f.environmentPath(request.WorkspaceId), data, 0o600)
}

func (f *fixture) workspaceSecretFrames(
	workspaceID string,
	exit *runtimev1.ExecServerMessage,
) ([]*runtimev1.ExecServerMessage, error) {
	// #nosec G304 -- Reads a hash-named fixture file in the test-owned directory.
	data, err := os.ReadFile(f.environmentPath(workspaceID))
	if err != nil {
		return nil, err
	}
	var environment map[string]string
	if err := json.Unmarshal(data, &environment); err != nil {
		return nil, err
	}
	return splitSecretFrames(environment["CUSTOM_VALUE"], exit), nil
}

func (f *fixture) Logs(
	request *runtimev1.LogsRequest,
	stream grpc.ServerStreamingServer[runtimev1.OutputChunk],
) error {
	switch f.mode {
	case "stream-logs-block":
		if err := os.WriteFile(filepath.Join(f.directory, "ready"), nil, 0o600); err != nil {
			return err
		}
		<-stream.Context().Done()
		return stream.Context().Err()
	case "stream-logs-empty":
		return stream.Send(&runtimev1.OutputChunk{})
	case "stream-logs-large":
		return stream.Send(&runtimev1.OutputChunk{Data: make([]byte, runtimev1.ChunkSize+1)})
	default:
		return f.Driver.Logs(request, stream)
	}
}
