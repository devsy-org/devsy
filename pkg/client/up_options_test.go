package client

import (
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/stretchr/testify/require"
)

type runtimeReporter struct {
	Callback func()
}

func (r runtimeReporter) Report(status.Event) { r.Callback() }

type runtimeReader struct {
	Callback func()
}

func (r runtimeReader) Read([]byte) (int, error) { r.Callback(); return 0, nil }

type runtimeWriter struct {
	Callback func()
}

func (w runtimeWriter) Write(p []byte) (int, error) { w.Callback(); return len(p), nil }

func TestUpOptionsJSONPreservesLevelAndExcludesRuntimeFields(t *testing.T) {
	for _, level := range []string{"warn", "info", "trace"} {
		t.Run(level, func(t *testing.T) {
			options := UpOptions{
				CLIOptions: provider.CLIOptions{SkipPostStart: true},
				LogLevel:   level,
				Stdin:      runtimeReader{Callback: func() {}},
				Stdout:     runtimeWriter{Callback: func() {}},
				Reporter:   runtimeReporter{Callback: func() {}},
			}
			encoded, err := json.Marshal(options)
			require.NoError(t, err)
			var decoded map[string]any
			require.NoError(t, json.Unmarshal(encoded, &decoded))
			require.Equal(t, level, decoded["logLevel"])
			require.Equal(t, true, decoded["skipPostStart"])
			require.NotContains(t, decoded, "Stdin")
			require.NotContains(t, decoded, "Stdout")
			require.NotContains(t, decoded, "Reporter")
		})
	}
}

func TestUpOptionsEffectiveLogLevel(t *testing.T) {
	log.InitTestObserved(t, log.SlogLevelTrace)
	level, err := (UpOptions{}).EffectiveLogLevel()
	require.NoError(t, err)
	require.Equal(t, "trace", level)
	const privateValue = "private-level-canary-82531"
	_, err = (UpOptions{LogLevel: privateValue}).EffectiveLogLevel()
	require.ErrorContains(t, err, "invalid log level")
	require.ErrorContains(t, err, "trace")
	require.NotContains(t, err.Error(), privateValue)
}
