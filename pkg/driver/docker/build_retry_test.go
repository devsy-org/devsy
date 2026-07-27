package docker

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestClassifyBuildError(t *testing.T) {
	t.Parallel()

	// The opaque error the internal buildkit strategy returns; the transient
	// signal lives only in the streamed output.
	solveErr := errors.New(
		"internal buildkit build: build: failed to solve: process " +
			`"/bin/sh -c ./devcontainer-features-install.sh" did not complete successfully: exit code: 1`,
	)
	incompleteReadOutput := "http.client.IncompleteRead: IncompleteRead(13384521 bytes read, 1002976 more expected)"

	tests := []struct {
		name   string
		err    error
		output string
		want   bool
	}{
		{name: "nil error", err: nil, want: false},
		{
			name:   "incomplete read in output",
			err:    solveErr,
			output: incompleteReadOutput,
			want:   true,
		},
		{
			name:   "genuine build failure",
			err:    solveErr,
			output: "npm ERR! missing script: build",
			want:   false,
		},
		{
			name: "context canceled is not transient",
			err:  fmt.Errorf("build: %w", context.Canceled),
			want: false,
		},
		{
			name: "deadline exceeded is not transient",
			err:  fmt.Errorf("build: %w", context.DeadlineExceeded),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			classified := classifyBuildError(tt.err, tt.output)
			if got := errors.Is(classified, errTransientBuild); got != tt.want {
				t.Errorf("errors.Is(classifyBuildError(), errTransientBuild) = %v, want %v", got, tt.want)
			}
			if tt.err != nil && !errors.Is(classified, tt.err) {
				t.Errorf("classifyBuildError() dropped the original error")
			}
		})
	}
}
