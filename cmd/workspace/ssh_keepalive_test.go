package workspace

import (
	"errors"
	"testing"
)

func TestCheckKeepAliveResponse(t *testing.T) {
	t.Parallel()

	transportErr := errors.New("connection closed")
	tests := []struct {
		name string
		ok   bool
		err  error
		want error
	}{
		{name: "positive reply", ok: true},
		{name: "negative reply", ok: false, want: errors.New("keepalive request rejected")},
		{name: "transport error", ok: true, err: transportErr, want: transportErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkKeepAliveResponse(tt.ok, tt.err)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("checkKeepAliveResponse() = %v, want nil", got)
				}
				return
			}
			if got == nil || got.Error() != tt.want.Error() {
				t.Fatalf("checkKeepAliveResponse() = %v, want %v", got, tt.want)
			}
		})
	}
}
