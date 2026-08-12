package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFailedBootSentinel(t *testing.T) {
	tests := []struct {
		name         string
		status       string
		graceElapsed bool
		wantSentinel error
		wantNil      bool
	}{
		{"dead is terminal regardless of grace", "dead", false, ErrContainerTerminal, false},
		{"dead is terminal even after grace", "dead", true, ErrContainerTerminal, false},
		{
			"removing is terminal regardless of grace",
			"removing",
			false,
			ErrContainerTerminal,
			false,
		},
		{"removing is terminal even after grace", "removing", true, ErrContainerTerminal, false},
		{"exited before grace is still booting", "exited", false, nil, true},
		{"exited after grace failed", "exited", true, ErrContainerExited, false},
		{"created before grace is still booting", "created", false, nil, true},
		{"created after grace failed", "created", true, ErrContainerExited, false},
		{"paused is not a terminal boot state", "paused", true, nil, true},
		{"restarting is not a terminal boot state", "restarting", false, nil, true},
		{"empty status is not a terminal boot state", "", true, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := failedBootSentinel(tt.status, tt.graceElapsed)
			if tt.wantNil {
				assert.Nil(t, got, "expected nil sentinel for status %q", tt.status)
				return
			}
			assert.ErrorIs(
				t,
				got,
				tt.wantSentinel,
				"status %q (grace=%v) should map to %v",
				tt.status,
				tt.graceElapsed,
				tt.wantSentinel,
			)
		})
	}
}

func TestFailedBootSentinel_TerminalNotConfusedWithExited(t *testing.T) {
	terminal := failedBootSentinel("removing", false)
	exited := failedBootSentinel("exited", true)

	assert.ErrorIs(t, terminal, ErrContainerTerminal)
	assert.NotErrorIs(t, terminal, ErrContainerExited,
		"terminal states must not be reported as exited")
	assert.ErrorIs(t, exited, ErrContainerExited)
	assert.NotErrorIs(t, exited, ErrContainerTerminal,
		"exited-after-grace must not be reported as terminal")
}
