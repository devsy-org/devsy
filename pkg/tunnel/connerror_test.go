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
		{"wrapped EOF", fmt.Errorf("ssh: handshake failed: %w", io.EOF), true},
		{"string containing EOF", errors.New("run in container: ssh: handshake failed: EOF"), true},
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

	tunnelErr := errors.New("host unreachable")
	handlerEOF := fmt.Errorf("ssh: handshake failed: %w", io.EOF)
	handlerNonEOF := errors.New("permission denied")

	tests := []struct {
		name      string
		tunnelErr error
		handler   error
		wantNil   bool
		wantMsg   string
	}{
		{"handler nil returns nil", tunnelErr, nil, true, ""},
		{
			"handler EOF + tunnel err returns tunnel",
			tunnelErr,
			handlerEOF,
			false,
			"connect to server: host unreachable",
		},
		{"handler EOF + no tunnel returns nil", nil, handlerEOF, true, ""},
		{
			"handler non-EOF returns handler error",
			nil,
			handlerNonEOF,
			false,
			"tunnel to container: permission denied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyTunnelErrors(tt.tunnelErr, tt.handler)
			if tt.wantNil {
				if got != nil {
					t.Errorf("ClassifyTunnelErrors() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("ClassifyTunnelErrors() = nil, want error")
			}
			if got.Error() != tt.wantMsg {
				t.Errorf("ClassifyTunnelErrors() = %q, want %q", got.Error(), tt.wantMsg)
			}
		})
	}
}
