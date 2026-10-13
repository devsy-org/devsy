package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/devsy-org/devsy-runtime-sdk/conformance/fake"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy-runtime-sdk/server"
	"github.com/devsy-org/devsy-runtime-sdk/supervisor"
	"github.com/shirou/gopsutil/v4/process"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "supervise" {
		recordPID(os.Args[2], "supervisor")
		supervisor.Main(os.Args[3:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "child" {
		recordPID(os.Args[2], "child")
		for {
			time.Sleep(time.Hour)
		}
	}
	mode := flag.String("mode", fake.Normal, "fixture mode")
	state := flag.String("state-dir", "", "state directory")
	nonce := flag.String("reply-nonce", "", "owned lost-reply test nonce")
	delay := flag.Duration("delay", 0, "handshake delay")
	flag.Parse()
	recordPID(*state, "plugin")
	serve(*mode, *state, *delay, *nonce)
}

func serve(mode, state string, delay time.Duration, nonce string) {
	if mode == fake.CrashBeforeHandshake {
		os.Exit(22)
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	if isLostReplyMode(mode) {
		if err := recordLostReplyProcesses(state, nonce); err != nil {
			panic(err)
		}
	}
	fixtureMode := underlyingFixtureMode(mode)
	runtime, err := fake.New(fake.Config{
		StateDir: state, Mode: fixtureMode, RecreateOnRun: mode == "recreate-on-run",
	})
	if err != nil {
		panic(err)
	}
	server.Serve(&fixture{Driver: runtime, mode: mode, directory: state, replyNonce: nonce})
}

func underlyingFixtureMode(mode string) string {
	switch mode {
	case "blocked",
		"environment",
		"block-info",
		"env-error",
		"workspace-identity",
		"recreate-on-run",
		persistRunThenExit,
		persistRunThenHold:
		return fake.Normal
	default:
		if strings.HasPrefix(mode, "stream-") {
			return fake.Normal
		}
		return mode
	}
}

func recordLostReplyProcesses(directory, nonce string) error {
	if nonce == "" {
		return errors.New("lost reply fixture requires an owned nonce")
	}
	for kind, pid := range map[string]int{"plugin": os.Getpid(), "supervisor": os.Getppid()} {
		identity, err := lostReplyProcessIdentity(pid, kind)
		if err != nil {
			return err
		}
		if err := appendLostReplyRecord(directory, "reply-processes.jsonl", identity); err != nil {
			return err
		}
	}
	return nil
}

type fixture struct {
	*fake.Driver
	mode, directory, replyNonce string
}

func (f *fixture) Info(
	ctx context.Context,
	request *runtimev1.InfoRequest,
) (*runtimev1.InfoResponse, error) {
	if f.mode == "block-info" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	response, err := f.Driver.Info(ctx, request)
	if err != nil {
		return nil, err
	}
	if f.mode == "environment" {
		if os.Getenv("DEVSY_RUNTIME_PLUGIN") != "devsy-runtime-v1" ||
			os.Getenv("FIXTURE_EMPTY") != "" ||
			os.Getenv("FIXTURE_TOKEN") != "private-canary-value" {
			return nil, fmt.Errorf("fixture environment was not preserved")
		}
		response.RuntimeVersion = os.Getenv("FIXTURE_SETTING")
	}
	return response, nil
}

func (f *fixture) Preflight(
	ctx context.Context,
	request *runtimev1.PreflightRequest,
) (*runtimev1.PreflightResponse, error) {
	if f.mode != "blocked" && f.mode != "stream-block-child" {
		return f.Driver.Preflight(ctx, request)
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// #nosec G204 -- Relaunches this trusted test fixture; no shell or PATH lookup.
	child := exec.Command(executable, "child", f.directory)
	if err := child.Start(); err != nil {
		return nil, err
	}
	go func() { _ = child.Wait() }()
	for {
		if _, err := os.Stat(filepath.Join(f.directory, "child-pids")); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(f.directory, "ready"), nil, 0o600); err != nil {
		return nil, err
	}
	// #nosec G118 -- Deliberately ignores cancellation to prove supervisor forced cleanup.
	for {
		time.Sleep(time.Hour)
	}
}

func (f *fixture) RunImage(
	ctx context.Context,
	request *runtimev1.RunImageRequest,
) (*runtimev1.RunImageResponse, error) {
	if isLostReplyMode(f.mode) {
		return f.runImageWithLostReply(ctx, request)
	}
	if f.mode == "workspace-identity" {
		request.Labels = append(request.Labels,
			"fixture.remote-user="+request.GetRemoteUser(),
			"fixture.dockerless="+strconv.FormatBool(request.GetDockerless()),
		)
	}
	if f.mode == "stream-workspace-secret" {
		if err := f.saveEnvironment(request); err != nil {
			return nil, err
		}
	}
	if f.mode != "env-error" {
		return f.Driver.RunImage(ctx, request)
	}
	secret := request.Environment["CUSTOM_VALUE"]
	failure, err := status.New(codes.Unavailable, "raw "+secret).
		WithDetails(&runtimev1.RuntimeError{
			Code:    runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNAVAILABLE,
			Message: "failure " + secret, RuntimeMessage: "backend " + secret, Retryable: true,
		})
	if err != nil {
		return nil, err
	}
	return nil, failure.Err()
}

func recordPID(directory, kind string) {
	// #nosec G304 G703 -- Fixture writes only into the test-owned temporary directory.
	file, err := os.OpenFile(
		filepath.Join(directory, kind+"-pids"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		panic(err)
	}
	_, err = fmt.Fprintln(file, strconv.Itoa(os.Getpid()))
	if err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
}

const (
	persistRunThenExit = "persist-run-then-exit"
	persistRunThenHold = "persist-run-then-hold"
	lostReplyExitCode  = 37
)

type replyProcessIdentity struct {
	PID     int32  `json:"pid"`
	Started int64  `json:"started"`
	Kind    string `json:"kind"`
}

type replyOperationRecord struct {
	Operation string `json:"operation"`
	Workspace string `json:"workspace"`
	Nonce     string `json:"nonce"`
}

type persistedReplyMarker struct {
	Workspace string                      `json:"workspace"`
	Nonce     string                      `json:"nonce"`
	Mode      string                      `json:"mode"`
	Container *runtimev1.ContainerDetails `json:"container"`
}

func isLostReplyMode(mode string) bool {
	return mode == persistRunThenExit || mode == persistRunThenHold
}

func lostReplyProcessIdentity(pid int, kind string) (replyProcessIdentity, error) {
	// #nosec G115 -- OS process identifiers fit the platform's signed PID representation.
	observed, err := process.NewProcess(int32(pid))
	if err != nil {
		return replyProcessIdentity{}, err
	}
	started, err := observed.CreateTime()
	if err != nil {
		return replyProcessIdentity{}, err
	}
	return replyProcessIdentity{PID: observed.Pid, Started: started, Kind: kind}, nil
}

func appendLostReplyRecord(directory, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	// #nosec G304 G703 -- Directory is owned by the test; name is a fixture literal.
	file, err := os.OpenFile(
		filepath.Join(directory, name),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	return errors.Join(writeErr, file.Close())
}

func (f *fixture) Stop(
	ctx context.Context,
	request *runtimev1.StopRequest,
) (*runtimev1.StopResponse, error) {
	if isLostReplyMode(f.mode) {
		if err := f.recordReplyOperation("stop", request.GetWorkspaceId()); err != nil {
			return nil, err
		}
	}
	return f.Driver.Stop(ctx, request)
}

func (f *fixture) Delete(
	ctx context.Context,
	request *runtimev1.DeleteRequest,
) (*runtimev1.DeleteResponse, error) {
	if isLostReplyMode(f.mode) {
		if err := f.recordReplyOperation("delete", request.GetWorkspaceId()); err != nil {
			return nil, err
		}
	}
	return f.Driver.Delete(ctx, request)
}

func (f *fixture) recordReplyOperation(operation, workspace string) error {
	return appendLostReplyRecord(f.directory, "reply-operations.jsonl", replyOperationRecord{
		Operation: operation, Workspace: workspace, Nonce: f.replyNonce,
	})
}

func (f *fixture) publishReplyMarker(value persistedReplyMarker) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(f.directory, ".reply-marker-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(f.directory, "reply-persisted.json"))
}

func (f *fixture) runImageWithLostReply(
	ctx context.Context,
	request *runtimev1.RunImageRequest,
) (*runtimev1.RunImageResponse, error) {
	if err := f.recordReplyOperation("attempt", request.GetWorkspaceId()); err != nil {
		return nil, err
	}
	if _, err := f.Driver.RunImage(ctx, request); err != nil {
		return nil, err
	}
	if err := f.recordReplyOperation("created", request.GetWorkspaceId()); err != nil {
		return nil, err
	}
	if err := f.publishCreatedReply(ctx, request.GetWorkspaceId()); err != nil {
		return nil, err
	}
	if f.mode == persistRunThenExit {
		os.Exit(lostReplyExitCode)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fixture) publishCreatedReply(ctx context.Context, workspace string) error {
	found, err := f.Find(ctx, &runtimev1.FindRequest{WorkspaceId: workspace})
	if err != nil {
		return err
	}
	if !found.GetFound() || found.GetContainer() == nil {
		return errors.New("created fixture record missing")
	}
	if err := f.publishReplyMarker(persistedReplyMarker{
		Workspace: workspace, Nonce: f.replyNonce, Mode: f.mode, Container: found.GetContainer(),
	}); err != nil {
		return err
	}
	return nil
}
