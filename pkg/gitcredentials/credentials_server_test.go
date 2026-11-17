package gitcredentials

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/stretchr/testify/require"
)

type credentialChildRecord struct {
	Args  []string `json:"args"`
	Input string   `json:"input"`
}

func TestMain(m *testing.M) {
	if os.Getenv("DEVSY_TEST_CREDENTIAL_CHILD") == "1" && len(os.Args) > 1 &&
		os.Args[1] == "internal" {
		input, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(91)
		}
		record, err := json.Marshal(credentialChildRecord{Args: os.Args[1:], Input: string(input)})
		if err != nil {
			os.Exit(92)
		}
		recordPath := os.Getenv("DEVSY_TEST_CREDENTIAL_RECORD")
		if err := os.WriteFile(recordPath, record, 0o600); err != nil {
			os.Exit(93)
		}
		if _, err := os.Stdout.WriteString(
			"username=alice\npassword=credential-value\n",
		); err != nil {
			os.Exit(94)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCredentialsViaServerForwardsEffectiveLevelAndProtocol(t *testing.T) {
	for _, tc := range []struct {
		level log.Level
		want  string
	}{
		{log.SlogLevelWarn, "warn"},
		{log.SlogLevelInfo, "info"},
		{log.SlogLevelTrace, "trace"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			log.InitTestObserved(t, tc.level)
			recordPath := filepath.Join(t.TempDir(), "request.json")
			t.Setenv("DEVSY_TEST_CREDENTIAL_CHILD", "1")
			t.Setenv("DEVSY_TEST_CREDENTIAL_RECORD", recordPath)
			request := &GitCredentials{Protocol: httpPathTestProtocol, Host: "example.com"}
			got, err := credentialsViaServer(context.Background(), request, "8022")
			require.NoError(t, err)
			require.Equal(t, "alice", got.Username)
			require.Equal(t, "credential-value", got.Password)
			data, err := os.ReadFile(recordPath)
			require.NoError(t, err)
			var record credentialChildRecord
			require.NoError(t, json.Unmarshal(data, &record))
			require.Equal(
				t,
				[]string{
					"internal",
					"agent",
					"git-credentials",
					"--port",
					"8022",
					"get",
					"--log-level",
					tc.want,
				},
				record.Args,
			)
			require.Equal(t, request.Encode(), record.Input)
		})
	}
}
