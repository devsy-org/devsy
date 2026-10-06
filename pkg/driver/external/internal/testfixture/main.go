package main

import (
	"context"
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
	delay := flag.Duration("delay", 0, "handshake delay")
	flag.Parse()
	recordPID(*state, "plugin")
	serve(*mode, *state, *delay)
}

func serve(mode, state string, delay time.Duration) {
	if mode == fake.CrashBeforeHandshake {
		os.Exit(22)
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	fixtureMode := mode
	if fixtureMode == "blocked" || fixtureMode == "environment" || fixtureMode == "block-info" ||
		fixtureMode == "env-error" || strings.HasPrefix(fixtureMode, "stream-") {
		fixtureMode = fake.Normal
	}
	runtime, err := fake.New(fake.Config{StateDir: state, Mode: fixtureMode})
	if err != nil {
		panic(err)
	}
	server.Serve(&fixture{Driver: runtime, mode: mode, directory: state})
}

type fixture struct {
	*fake.Driver
	mode, directory string
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
