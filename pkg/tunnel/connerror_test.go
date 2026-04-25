package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestIsEOF(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"bare io.EOF", io.EOF, true},
		{"wrapped EOF", fmt.Errorf("read: %w", io.EOF), true},
		{"string containing EOF", errors.New("ssh: handshake failed: EOF"), true},
		{"non-EOF error", errors.New("connection refused"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsEOF(tt.err); got != tt.want {
				t.Errorf("IsEOF(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassifyError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want ErrorKind
	}{
		{"nil", nil, ErrorShutdown},
		{"context.Canceled", context.Canceled, ErrorShutdown},
		{"context.DeadlineExceeded", context.DeadlineExceeded, ErrorShutdown},
		{"io.EOF", io.EOF, ErrorTransient},
		{"wrapped EOF", fmt.Errorf("read: %w", io.EOF), ErrorTransient},
		{"random error", errors.New("something broke"), ErrorPermanent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ClassifyError(tt.err); got != tt.want {
				t.Errorf("ClassifyError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassifyTunnelErrors(t *testing.T) {
	t.Parallel()
	tunnelErr := errors.New("tunnel died")
	handlerEOF := fmt.Errorf("read: %w", io.EOF)
	handlerNonEOF := errors.New("handler broke")

	tests := []struct {
		name       string
		tunnelErr  error
		handlerErr error
		wantNil    bool
		wantSubstr string
	}{
		{"handler nil", tunnelErr, nil, true, ""},
		{"handler EOF + tunnel err", tunnelErr, handlerEOF, false, "connect to server"},
		{"handler EOF + no tunnel", nil, handlerEOF, true, ""},
		{"handler non-EOF", nil, handlerNonEOF, false, "tunnel to container"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyTunnelErrors(tt.tunnelErr, tt.handlerErr)
			if tt.wantNil {
				if got != nil {
					t.Errorf("ClassifyTunnelErrors() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("ClassifyTunnelErrors() = nil, want error containing %q", tt.wantSubstr)
			}
			if msg := got.Error(); !contains(msg, tt.wantSubstr) {
				t.Errorf("ClassifyTunnelErrors() = %q, want substring %q", msg, tt.wantSubstr)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(substr) == 0 || len(s) >= len(substr) && containsAt(s, substr)
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
