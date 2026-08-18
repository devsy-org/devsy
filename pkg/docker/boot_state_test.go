package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	statusDead       = "dead"
	statusRemoving   = "removing"
	statusExited     = "exited"
	statusCreated    = "created"
	statusPaused     = "paused"
	statusRestarting = "restarting"
)

func TestFailedBootSentinel(t *testing.T) {
	tests := []struct {
		name         string
		status       string
		graceElapsed bool
		wantSentinel error
		wantNil      bool
	}{
		{"dead is terminal regardless of grace", statusDead, false, ErrContainerTerminal, false},
		{"dead is terminal even after grace", statusDead, true, ErrContainerTerminal, false},
		{
			"removing is terminal regardless of grace",
			statusRemoving,
			false,
			ErrContainerTerminal,
			false,
		},
		{
			"removing is terminal even after grace",
			statusRemoving,
			true,
			ErrContainerTerminal,
			false,
		},
		{"exited before grace is still booting", statusExited, false, nil, true},
		{"exited after grace failed", statusExited, true, ErrContainerExited, false},
		{"created before grace is still booting", statusCreated, false, nil, true},
		{"created after grace failed", statusCreated, true, ErrContainerExited, false},
		{"paused is not a terminal boot state", statusPaused, true, nil, true},
		{"restarting is not a terminal boot state", statusRestarting, false, nil, true},
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
	terminal := failedBootSentinel(statusRemoving, false)
	exited := failedBootSentinel(statusExited, true)

	assert.ErrorIs(t, terminal, ErrContainerTerminal)
	assert.NotErrorIs(t, terminal, ErrContainerExited,
		"terminal states must not be reported as exited")
	assert.ErrorIs(t, exited, ErrContainerExited)
	assert.NotErrorIs(t, exited, ErrContainerTerminal,
		"exited-after-grace must not be reported as terminal")
}
