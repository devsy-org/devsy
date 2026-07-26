package buildkit

import (
	"strings"
	"testing"
)

func TestParseBuildSecrets(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		want    map[string]string
		wantErr bool
	}{
		{
			name:    "empty",
			entries: nil,
			want:    map[string]string{},
		},
		{
			name:    "single",
			entries: []string{"TOKEN=abc123"},
			want:    map[string]string{"TOKEN": "abc123"},
		},
		{
			name:    "value with equals",
			entries: []string{"URL=key=value&x=y"},
			want:    map[string]string{"URL": "key=value&x=y"},
		},
		{
			name:    "empty value",
			entries: []string{"EMPTY="},
			want:    map[string]string{"EMPTY": ""},
		},
		{
			name:    "missing equals",
			entries: []string{"NOEQUALS"},
			wantErr: true,
		},
		{
			name:    "empty name",
			entries: []string{"=value"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseBuildSecrets(tt.entries)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				assertNoValueLeak(t, err, tt.entries)
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertSecrets(t, got, tt.want)
		})
	}
}

func assertSecrets(t *testing.T, got map[string][]byte, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d secrets, want %d", len(got), len(want))
	}
	for k, v := range want {
		gotVal, ok := got[k]
		if !ok {
			t.Errorf("secret %q missing from result", k)
			continue
		}
		if string(gotVal) != v {
			t.Errorf("secret %q = %q, want %q", k, gotVal, v)
		}
	}
}

func assertNoValueLeak(t *testing.T, err error, entries []string) {
	t.Helper()
	for _, entry := range entries {
		if _, value, ok := strings.Cut(entry, "="); ok && value != "" &&
			strings.Contains(err.Error(), value) {
			t.Errorf("error %q leaked secret value %q", err, value)
		}
	}
}

func TestBuildSecretsAttachable(t *testing.T) {
	attachable, err := buildSecretsAttachable(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attachable != nil {
		t.Fatalf("expected nil attachable for empty secrets")
	}

	attachable, err = buildSecretsAttachable([]string{"TOKEN=abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attachable == nil {
		t.Fatalf("expected non-nil attachable for non-empty secrets")
	}

	if _, err := buildSecretsAttachable([]string{"invalid"}); err == nil {
		t.Fatalf("expected error for invalid entry")
	}
}
