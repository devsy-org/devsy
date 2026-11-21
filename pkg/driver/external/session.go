package external

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"slices"
	"sync"
	"time"

	sdkplugin "github.com/devsy-org/devsy-runtime-sdk/plugin"
	"github.com/devsy-org/devsy-runtime-sdk/runtimev1"
	"github.com/devsy-org/devsy-runtime-sdk/supervisor"
	"github.com/devsy-org/devsy/logging/redact"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/hashicorp/go-hclog"
	hplugin "github.com/hashicorp/go-plugin"
	"github.com/hashicorp/go-plugin/runner"
	"google.golang.org/grpc/status"
)

func (h *Host) call(
	ctx context.Context,
	operation string,
	rpc func(runtimev1.RuntimeDriverClient) error,
) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	binary, err := h.executable()
	if err != nil {
		return fmt.Errorf("external runtime %s: %w", operation, err)
	}
	var rpcDuration time.Duration
	var called bool
	defer func() {
		metricErr := err
		if err != nil && ctx.Err() != nil {
			metricErr = ctx.Err()
		}
		code := runtimeMetricCode(metricErr)
		err = h.operationError(ctx, operation, err)
		if called {
			telemetry.RecordProviderOperation(ctx, telemetry.ProviderOperation{
				ProviderType: telemetry.ProviderTypeExternalSDK,
				Operation:    operation,
				Duration:     rpcDuration,
				ErrorCode:    code,
			})
		}
	}()
	attrs := []slog.Attr{log.AttrComponent("external-driver")}
	if h.providerName != "" {
		attrs = append(attrs, log.AttrProvider(h.providerName))
	}
	output := log.NewJSONLogStreamer(log.StreamerOptions{
		Redactor:      h.redactor,
		FallbackLevel: slog.LevelDebug,
		DefaultAttrs:  attrs,
	})
	diagnostic := &diagnosticWriter{
		writer: output,
		stream: redact.NewStream(h.redactor),
	}
	defer func() { diagnostic.close(); _ = output.Close() }()
	client := h.newPluginClient(ctx, binary, diagnostic)
	defer client.Kill()
	started := time.Now()
	transport, err := client.Client()
	if err != nil {
		return err
	}
	instance, err := transport.Dispense(sdkplugin.Name)
	if err != nil {
		return err
	}
	runtimeClient, ok := instance.(runtimev1.RuntimeDriverClient)
	if !ok {
		return errors.New("invalid gRPC client")
	}
	log.DebugContext(ctx, "External runtime startup completed",
		log.AttrOperation(operation), log.AttrDurationMs(time.Since(started).Milliseconds()))
	called = true
	rpcStart := time.Now()
	err = rpc(runtimeClient)
	rpcDuration = time.Since(rpcStart)
	return err
}

func runtimeMetricCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var category runtimev1.RuntimeErrorCode
	if runtimeErr, ok := errors.AsType[*RuntimeError](err); ok {
		category = runtimeErr.Category
	} else if rpcStatus, ok := status.FromError(err); ok {
		for _, detail := range rpcStatus.Details() {
			if runtimeErr, ok := detail.(*runtimev1.RuntimeError); ok {
				category = runtimeErr.Code
				break
			}
		}
	}
	return runtimeCategoryMetricCode(category)
}

func runtimeCategoryMetricCode(category runtimev1.RuntimeErrorCode) string {
	switch category {
	case runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_NOT_FOUND:
		return "not_found"
	case runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_ALREADY_EXISTS:
		return "already_exists"
	case runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_INVALID_ARGUMENT:
		return "invalid_argument"
	case runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNSUPPORTED:
		return "unsupported"
	case runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_UNAVAILABLE:
		return "unavailable"
	case runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_FAILED_PRECONDITION:
		return "failed_precondition"
	default:
		return runtimeCategoryMetricCodeOther(category)
	}
}

func runtimeCategoryMetricCodeOther(category runtimev1.RuntimeErrorCode) string {
	switch category {
	case runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_PERMISSION_DENIED:
		return "permission_denied"
	case runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_TIMEOUT:
		return "timeout"
	case runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_CANCELLED:
		return "cancelled"
	case runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_RUNTIME_FAILURE:
		return "runtime_failure"
	case runtimev1.RuntimeErrorCode_RUNTIME_ERROR_CODE_INCOMPATIBLE_VERSION:
		return "incompatible_version"
	default:
		return "unknown"
	}
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
	stream *redact.Stream
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

func (h *Host) newPluginClient(
	ctx context.Context,
	binary string,
	diagnostic *diagnosticWriter,
) *hplugin.Client {
	owned := supervisor.Runner(supervisor.Options{
		SupervisorBinary: h.supervisorBinary, SupervisorArgs: h.supervisorArgs,
		RuntimeBinary: binary, Args: h.config.External.Args,
		Env: append(slices.Clone(h.environment), h.config.External.Binary+"="+binary),
	})
	// Routine helper lifecycle diagnostics belong at trace; runtime stderr is
	// preserved independently below, including unstructured backend failures.
	frameworkLogLevel := hclog.Warn
	if log.LevelString() == "trace" {
		frameworkLogLevel = hclog.Trace
	}
	return hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig: sdkplugin.Handshake(),
		VersionedPlugins: map[int]hplugin.PluginSet{
			sdkplugin.ProtocolVersion: sdkplugin.ClientPlugins(),
		},
		AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolGRPC},
		StartTimeout:     h.timeout,
		Logger: hclog.New(&hclog.LoggerOptions{
			Output: diagnostic, Level: frameworkLogLevel,
		}),
		Stderr: diagnostic,
		RunnerFunc: func(logger hclog.Logger, command *exec.Cmd, socketDir string) (runner.Runner, error) {
			process, err := owned(logger, command, socketDir)
			if err != nil {
				return nil, err
			}
			return &contextRunner{Runner: process, caller: ctx}, nil
		},
	})
}
