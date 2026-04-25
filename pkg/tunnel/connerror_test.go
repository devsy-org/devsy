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
		{"nil input", nil, false},
		{"bare io.EOF", io.EOF, true},
		{"wrapped EOF", fmt.Errorf("read failed: %w", io.EOF), true},
		{"string containing EOF", errors.New("connection closed: EOF"), true},
		{"non-EOF", errors.New("timeout"), false},
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

	tunnelErr := errors.New("dial failed")
	handlerNonEOF := errors.New("handler broke")

	tests := []struct {
		name       string
		tunnelErr  error
		handlerErr error
		wantNil    bool
		wantSubstr string
	}{
		{"handler nil", tunnelErr, nil, true, ""},
		{"handler EOF with tunnel err", tunnelErr, io.EOF, false, "connect to server: dial failed"},
		{"handler EOF no tunnel err", nil, io.EOF, true, ""},
		{"handler non-EOF", nil, handlerNonEOF, false, "tunnel to container: handler broke"},
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
				t.Fatal("ClassifyTunnelErrors() = nil, want error")
			}
			if got.Error() != tt.wantSubstr {
				t.Errorf("ClassifyTunnelErrors() = %q, want %q", got.Error(), tt.wantSubstr)
			}
		})
	}
}
