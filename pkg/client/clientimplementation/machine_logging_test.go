package clientimplementation

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/require"
)

const testShellExitSuccess = "exit 0"

func machineLoggingClient(t *testing.T, command string) client.MachineClient {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	cfg := &provider.ProviderConfig{
		Name: testCancelProvider,
		Exec: provider.ProviderCommands{
			Create: types.StrArray{"/bin/sh", "-c", testShellExitSuccess},
			Status: types.StrArray{"/bin/sh", "-c", command},
		},
	}
	devsy := &config.Config{
		DefaultContext: testLocksContext,
		Contexts:       map[string]*config.ContextConfig{testLocksContext: {}},
	}
	machine, err := NewMachineClient(
		devsy,
		cfg,
		&provider.Machine{ID: "machine-canary", Context: testLocksContext},
	)
	require.NoError(t, err)
	return machine
}

func TestMachineStatusPreservesProtocolAndRecordsMetrics(t *testing.T) {
	observed := log.InitTestObserved(t, log.SlogLevelInfo)
	machine := machineLoggingClient(
		t,
		`printf 'Running'; printf '%s\n' '{"level":"info","msg":"diagnostic canary","custom":"value"}' >&2`,
	)
	reporter := status.NewMemoryReporter()
	metrics := telemetry.NewMetricsReporter()
	ctx := telemetry.WithMetrics(status.WithReporter(context.Background(), reporter), metrics)
	result, err := machine.Status(ctx, client.StatusOptions{})
	require.NoError(t, err)
	require.Equal(t, client.Status(client.StatusRunning), result)
	require.NotEmpty(t, observed.FilterMessage("diagnostic canary").All())
	require.Equal(t, status.PhaseMachineStatus, reporter.Events()[0].Phase)
	snapshot := metrics.Finish(nil)
	require.NotEmpty(t, snapshot.ProviderOperations)
	require.Equal(t, "status", snapshot.ProviderOperations[0].Operation)
}

func TestMachineStatusRedactsDiagnosticAndPreservesExit(t *testing.T) {
	t.Setenv("MACHINE_TEST_TOKEN", "private-machine-secret")
	observed := log.InitTestObserved(t, log.SlogLevelInfo)
	machine := machineLoggingClient(
		t,
		`printf '%s\n' '{"level":"error","msg":"private-machine-secret",`+
			`"error_code":"aws_quota_exceeded"}' >&2; exit 7`,
	)
	_, err := machine.Status(context.Background(), client.StatusOptions{})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-machine-secret")
	var exit *exec.ExitError
	require.True(t, errors.As(err, &exit))
	require.Equal(t, 7, exit.ExitCode())
	require.Equal(t, clierr.CodeUnknown, clierr.Classify(err).Code)
	entries := observed.FilterAttr(slog.String("error_code", "aws_quota_exceeded")).All()
	require.Len(t, entries, 1)
	require.NotContains(t, entries[0].Message, "private-machine-secret")
}
