package redact

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSafeJSONPreservesHookOutputBytes(t *testing.T) {
	redactor := New(Options{Values: []string{"registered-fixture-secret"}})
	lines := []string{
		`{"kind":"status","schemaVersion":1,"id":"op-99"}`,
		`  { "kind": "result", "runId": "not-a-run", "nested": [null,true,9,{"z":0,"a":1}] }  `,
	}
	for _, line := range lines {
		if got := redactor.Redact(line); got != line {
			t.Fatalf("ordinary JSON log record changed: %q", got)
		}
		assertAllStreamCuts(t, redactor, line+"\r\n", line+"\r\n")
	}
}

func TestJSONSourcePreservationRejectsOverwrittenValues(t *testing.T) {
	const hidden = "synthetic-credential"
	redactor := New(Options{})
	lines := []string{
		`{"hook":"Authorization: ` + hidden + `","hook":"public"}`,
		`{"hook":{"\u0041uthorization":"` + hidden + `"},"\u0068ook":"public"}`,
		`{"list":[{"source":"https://user:` + hidden + `@example.test","source":"public"}]}`,
	}
	for _, line := range lines {
		got := redactor.Redact(line)
		if !json.Valid([]byte(got)) || strings.Contains(got, hidden) ||
			!strings.Contains(got, "public") {
			t.Fatalf("overwritten value leaked or safe content was lost: %q", got)
		}
		assertAllStreamCuts(t, redactor, line+"\n", got+"\n")
	}
}
