package provider

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestVersionsCommandJSONReturnsProviderVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/foo/versions.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(
			w,
			`{"versions":[{"tag":"v1.0.0","published_at":"2024-01-02T03:04:05Z","prerelease":false},`+
				`{"tag":"v2.0.0-beta","published_at":"2025-01-02T03:04:05Z","prerelease":true}]}`,
		)
	}))
	defer server.Close()
	t.Setenv(config.EnvHome, t.TempDir())
	cfg := setupProviderOperationTest(t)
	cfg.Current().Providers["fixture"] = &config.ProviderConfig{}
	require.NoError(t, config.SaveConfig(cfg))
	require.NoError(t, provider2.SaveProviderConfig(cfg.DefaultContext, &provider2.ProviderConfig{
		Name:   "fixture",
		Source: provider2.ProviderSource{URL: server.URL + "/foo/provider.yaml"},
		Exec:   provider2.ProviderCommands{Command: []string{"unused"}},
	}))
	read, write, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = read.Close() }()
	previous := os.Stdout
	os.Stdout = write
	t.Cleanup(func() { os.Stdout = previous })
	command := NewVersionsCmd(&flags.GlobalFlags{ResultFormat: "json"})
	command.SetArgs([]string{"fixture", "--no-cache", "--prerelease"})
	err = command.Execute()
	require.NoError(t, write.Close())
	os.Stdout = previous
	require.NoError(t, err)
	got, err := io.ReadAll(read)
	require.NoError(t, err)
	require.JSONEq(
		t,
		`[{"tag":"v2.0.0-beta","publishedAt":"2025-01-02T03:04:05Z","prerelease":true,"current":false},`+
			`{"tag":"v1.0.0","publishedAt":"2024-01-02T03:04:05Z","prerelease":false,"current":false}]`,
		string(got),
	)
}
