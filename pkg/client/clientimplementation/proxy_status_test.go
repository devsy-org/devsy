package clientimplementation

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"testing"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func proxyStatusFixture(t *testing.T, script string) *proxyClient {
	t.Helper()
	requirePOSIXShell(t)
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv("EXTERNAL_TOKEN", "status-host-secret-78231")
	t.Setenv("STATE_TOKEN", "RUNNING")
	const contextName = "default"
	p := &proxyClient{
		devsyConfig: &config.Config{
			DefaultContext: contextName,
			Contexts: map[string]*config.ContextConfig{
				contextName: {Providers: map[string]*config.ProviderConfig{
					"configured-alias": {Options: map[string]config.OptionValue{
						"OPAQUE": {Value: "status-option-secret-78232"},
					}},
				}},
			},
		},
		config: &provider.ProviderConfig{
			Name:    "configured-alias",
			Options: map[string]*types.Option{"OPAQUE": {Password: true}},
			Exec: provider.ProviderCommands{Proxy: &provider.ProxyCommands{
				Status: types.StrArray{binSh, "-c", script},
			}},
		},
		workspace: &provider.Workspace{ID: testProxyWorkspaceID, Context: contextName},
	}
	p.executor = &proxyExecutor{client: p}
	return p
}

func TestProxyStatusSeparatesProtocolFromRedactedDiagnostics(t *testing.T) {
	observed := log.InitTestObserved(t, log.SlogLevelInfo)
	p := proxyStatusFixture(
		t,
		`printf '%s' '{"state":"RUNNING","level":"error","msg":"stdout-only-canary"}'; `+
			`printf '{"level":"info","msg":"stderr-canary %s %s","phase":"readiness"}' `+
			`"$OPAQUE" "$EXTERNAL_TOKEN" >&2`,
	)
	got, err := p.Status(context.Background(), client.StatusOptions{})
	require.NoError(t, err)
	require.Equal(t, client.Status(client.StatusRunning), got)
	require.Zero(t, observed.FilterMessageSnippet("stdout-only-canary").Len())
	entries := observed.FilterMessageSnippet("stderr-canary").All()
	require.Len(t, entries, 1)
	assert.NotContains(t, entries[0].Message, "status-host-secret-78231")
	assert.NotContains(t, entries[0].Message, "status-option-secret-78232")
	assert.Contains(t, entries[0].Message, "***")
	assert.Equal(t, "configured-alias", entries[0].AttrsMap()["provider"])
	assert.Equal(t, "readiness", entries[0].AttrsMap()["phase"])
}

func TestProxyStatusFailureRetainsOnlyRedactedStderrAndCause(t *testing.T) {
	p := proxyStatusFixture(
		t,
		`printf 'stdout-private-canary'; printf 'stderr-canary %s %s' "$OPAQUE" "$EXTERNAL_TOKEN" >&2; exit 23`,
	)
	_, err := p.Status(context.Background(), client.StatusOptions{})
	require.Error(t, err)
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr))
	assert.Equal(t, 23, exitErr.ExitCode())
	classified := clierr.Classify(err)
	assert.Equal(t, clierr.CodeUnknown, classified.Code)
	assert.Contains(t, classified.Message, "stderr-canary")
	assert.Contains(t, classified.Message, "***")
	assert.NotContains(t, classified.Message, "stdout-private-canary")
	assert.NotContains(t, classified.Message, "status-host-secret-78231")
	assert.NotContains(t, classified.Message, "status-option-secret-78232")
}

func TestProxyStatusDecodeErrorDoesNotEchoProtocol(t *testing.T) {
	p := proxyStatusFixture(t, `printf 'stdout-private-canary'; printf 'stderr-canary' >&2`)
	_, err := p.Status(context.Background(), client.StatusOptions{})
	require.Error(t, err)
	var syntaxErr *json.SyntaxError
	require.True(t, errors.As(err, &syntaxErr))
	assert.NotContains(t, err.Error(), "stdout-private-canary")
	assert.Contains(t, clierr.Classify(err).Context["stderr"], "stderr-canary")
}

func TestProxyStatusCancellationKeepsCause(t *testing.T) {
	p := proxyStatusFixture(t, testShellExitSuccess)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Status(ctx, client.StatusOptions{})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, clierr.CodeCanceled, clierr.Classify(err).Code)
}
