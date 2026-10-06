package external

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"sync"
	"time"

	sdkplugin "github.com/devsy-org/devsy-runtime-sdk/plugin"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy-runtime-sdk/supervisor"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/hashicorp/go-hclog"
	hplugin "github.com/hashicorp/go-plugin"
	"github.com/hashicorp/go-plugin/runner"
)

func (h *Host) call(
	ctx context.Context,
	operation string,
	rpc func(runtimev1.RuntimeDriverClient) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	binary, err := h.executable()
	if err != nil {
		return fmt.Errorf("external runtime %s: %w", operation, err)
	}
	output := log.Writer(log.LevelDebug)
	diagnostic := &diagnosticWriter{
		writer: output,
		stream: secrets.NewStreamingRedactor(h.redactor),
	}
	defer func() { diagnostic.close(); _ = output.Close() }()
	owned := supervisor.Runner(supervisor.Options{
		SupervisorBinary: h.supervisorBinary, SupervisorArgs: h.supervisorArgs,
		RuntimeBinary: binary, Args: h.config.External.Args,
		Env: append(slices.Clone(h.environment), h.config.External.Binary+"="+binary),
	})
	client := hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig: sdkplugin.Handshake(),
		VersionedPlugins: map[int]hplugin.PluginSet{
			sdkplugin.ProtocolVersion: sdkplugin.ClientPlugins(),
		},
		AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolGRPC},
		StartTimeout:     h.timeout,
		Logger:           hclog.New(&hclog.LoggerOptions{Output: diagnostic, Level: hclog.Debug}),
		Stderr:           diagnostic,
		RunnerFunc: func(logger hclog.Logger, command *exec.Cmd, socketDir string) (runner.Runner, error) {
			process, err := owned(logger, command, socketDir)
			if err != nil {
				return nil, err
			}
			return &contextRunner{Runner: process, caller: ctx}, nil
		},
	})
	defer client.Kill()
	started := time.Now()
	transport, err := client.Client()
	if err != nil {
		return h.operationError(ctx, operation, err)
	}
	instance, err := transport.Dispense(sdkplugin.Name)
	if err != nil {
		return h.operationError(ctx, operation, err)
	}
	runtimeClient, ok := instance.(runtimev1.RuntimeDriverClient)
	if !ok {
		return fmt.Errorf("external runtime %s: invalid gRPC client", operation)
	}
	log.Debugf("External runtime %s startup completed in %s", operation, time.Since(started))
	return h.operationError(ctx, operation, rpc(runtimeClient))
}

// go-plugin holds its client lock throughout handshake, so Client.Kill cannot
// interrupt that phase. Cancel the leased runner directly instead.
type contextRunner struct {
	runner.Runner
	caller context.Context
}

func (r *contextRunner) Start(ctx context.Context) error {
	bounded, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.caller, cancel)
	defer stop()
	defer cancel()
	if err := r.Runner.Start(bounded); err != nil {
		return err
	}
	stopOwnership := context.AfterFunc(r.caller, func() { _ = r.Kill(context.Background()) })
	// #nosec G118 -- Reaping must outlive caller cancellation; the lease is closed by the callback.
	go func() { _ = r.Wait(context.Background()); stopOwnership() }()
	return nil
}

type diagnosticWriter struct {
	mu     sync.Mutex
	writer io.Writer
	stream *secrets.StreamingRedactor
}

func (w *diagnosticWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	text := w.stream.RedactChunk(string(data))
	_, err := io.WriteString(w.writer, text)
	return len(data), err
}

func (w *diagnosticWriter) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = io.WriteString(w.writer, w.stream.Flush())
}
