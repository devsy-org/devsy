package proxycmd

import (
	"errors"
	"strings"
	"testing"
)

var testHeaders = []string{"Name"}

func TestPrintTable_EmptyPayloadSkipsBuildRows(t *testing.T) {
	called := false
	err := printTable(testHeaders, nil, func([]byte) ([][]string, error) {
		called = true
		return nil, nil
	})
	if err != nil {
		t.Fatalf("printTable(empty) returned error: %v", err)
	}
	if called {
		t.Fatal("printTable must not call buildRows for an empty payload")
	}
}

func TestPrintTable_NonEmptyPayloadCallsBuildRows(t *testing.T) {
	payload := []byte(`[{"name":"a"}]`)
	var got []byte
	err := printTable(testHeaders, payload, func(p []byte) ([][]string, error) {
		got = p
		return [][]string{{"a"}}, nil
	})
	if err != nil {
		t.Fatalf("printTable(non-empty) returned error: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("buildRows got payload %q, want %q", got, payload)
	}
}

func TestPrintTable_BuildRowsErrorIsWrappedWithAction(t *testing.T) {
	wantCause := errors.New("bad json")
	err := printTable(
		testHeaders,
		[]byte(`not json`),
		func([]byte) ([][]string, error) {
			return nil, wantCause
		},
	)
	if err == nil {
		t.Fatal("printTable must return an error when buildRows fails")
	}
	if !errors.Is(err, wantCause) {
		t.Fatalf("printTable error does not wrap the buildRows cause: %v", err)
	}
	const wantSubstr = "parse output"
	if got := err.Error(); !strings.Contains(got, wantSubstr) {
		t.Fatalf("printTable error %q does not mention %q", got, wantSubstr)
	}
}
