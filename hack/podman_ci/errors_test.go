package main

import (
	"errors"
	"testing"
)

func TestExitCodeForError(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "cobra usage error", err: errors.New("required flag not set"), want: 2},
		{name: "typed usage error", err: classifySetupError(usageError("invalid mode")), want: 2},
		{
			name: "operational error",
			err:  classifySetupError(errors.New("setup failed")),
			want: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := exitCodeForError(test.err); got != test.want {
				t.Fatalf("exitCodeForError(%v) = %d, want %d", test.err, got, test.want)
			}
		})
	}
}
