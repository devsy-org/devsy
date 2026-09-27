//go:build linux

package command

import (
	"os"
	"syscall"
	"testing"
)

const processStatOpenOperation = "open"

func TestLinuxProcessStatErrorIsAbsent(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "process disappeared",
			err:  &os.PathError{Op: processStatOpenOperation, Err: syscall.ESRCH},
			want: true,
		},
		{
			name: "missing proc entry",
			err:  &os.PathError{Op: processStatOpenOperation, Err: os.ErrNotExist},
			want: true,
		},
		{
			name: "permission denied",
			err:  &os.PathError{Op: processStatOpenOperation, Err: os.ErrPermission},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := linuxProcessStatErrorIsAbsent(test.err); got != test.want {
				t.Fatalf(
					"linuxProcessStatErrorIsAbsent(%v) = %t, want %t",
					test.err,
					got,
					test.want,
				)
			}
		})
	}
}
