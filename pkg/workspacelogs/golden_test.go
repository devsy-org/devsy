package workspacelogs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

const goldenDir = "../../desktop/src/main/__tests__/fixtures"

// TestGoldenResponsesMatchDesktopFixtures keeps the JSON the Desktop parses in
// step with what the CLI emits. Regenerate with UPDATE_GOLDEN=1.
func TestGoldenResponsesMatchDesktopFixtures(t *testing.T) {
	s, _ := newTestStore(t)
	h := newHookRun(t, s)
	read, err := s.Read(ReadRequest{RunID: h.runID})
	require.NoError(t, err)
	index, err := s.ListRuns(IndexRequest{Context: testContext})
	require.NoError(t, err)

	for name, value := range map[string]any{
		"go-read-response.json":  normalize(t, read),
		"go-index-response.json": normalize(t, index),
	} {
		got, err := json.MarshalIndent(value, "", "  ")
		require.NoError(t, err)
		path := filepath.Join(goldenDir, name)
		if os.Getenv("UPDATE_GOLDEN") != "" {
			require.NoError(t, os.WriteFile(path, append(got, '\n'), 0o600))
		}
		want, err := os.ReadFile(path) //nolint:gosec // fixed test fixture path
		require.NoError(t, err, "run with UPDATE_GOLDEN=1 to create the fixture")
		require.JSONEq(t, string(want), string(got))
	}
}

// normalize replaces random IDs and times with stable values so the fixture
// does not change between runs.
func normalize(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var generic any
	require.NoError(t, json.Unmarshal(raw, &generic))
	ids := map[string]string{}

	return stabilize(generic, ids)
}

func stabilize(v any, ids map[string]string) any {
	switch x := v.(type) {
	case map[string]any:
		return stabilizeMap(x, ids)
	case []any:
		for i, val := range x {
			x[i] = stabilize(val, ids)
		}
	case string:
		return stabilizeString(x, ids)
	}

	return v
}

func stabilizeMap(m map[string]any, ids map[string]string) map[string]any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "nextCursor" {
			m[k] = "opaque-cursor"

			continue
		}
		m[k] = stabilize(m[k], ids)
	}

	return m
}

func stabilizeString(s string, ids map[string]string) string {
	if ValidID(s) {
		if _, ok := ids[s]; !ok {
			ids[s] = stableID(len(ids) + 1)
		}

		return ids[s]
	}
	if len(s) > 10 && s[4] == '-' && s[10] == 'T' {
		return "2026-10-30T12:00:00Z"
	}

	return s
}

func stableID(n int) string {
	return "00000000-0000-4000-8000-" + padHex(n)
}

func padHex(n int) string {
	const digits = "0123456789abcdef"
	out := []byte("000000000000")
	for i := len(out) - 1; i >= 0 && n > 0; i-- {
		out[i] = digits[n%16]
		n /= 16
	}

	return string(out)
}
