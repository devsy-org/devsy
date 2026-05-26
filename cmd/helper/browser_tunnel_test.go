package helper

import (
	"strings"
	"testing"
)

func TestParseInheritedListenerEntry_Errors(t *testing.T) {
	tests := []struct {
		name      string
		entry     string
		wantInErr string
	}{
		{
			name:      "no equals sign",
			entry:     "no-equals-sign",
			wantInErr: "expected host:port=fd",
		},
		{
			name:      "non-numeric fd",
			entry:     "localhost:10800=notanumber",
			wantInErr: "invalid fd",
		},
		{
			name:      "negative fd",
			entry:     "localhost:10800=-1",
			wantInErr: "must be >= 3",
		},
		{
			name:      "stdio fd (must be >= 3)",
			entry:     "localhost:10800=2",
			wantInErr: "must be >= 3",
		},
		{
			name:      "empty host",
			entry:     "=3",
			wantInErr: "empty host:port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, l, err := parseInheritedListenerEntry(tt.entry)
			if err == nil {
				if l != nil {
					_ = l.Close()
				}
				t.Fatalf("expected error containing %q, got nil (host=%q)", tt.wantInErr, host)
			}
			if l != nil {
				_ = l.Close()
				t.Errorf("expected nil listener on error, got non-nil")
			}
			if host != "" {
				t.Errorf("expected empty host on error, got %q", host)
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantInErr)
			}
		})
	}
}
