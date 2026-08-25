package docker

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/assert"
)

func TestFailedBootSentinel(t *testing.T) {
	tests := []struct {
		name         string
		status       config.ContainerStatus
		graceElapsed bool
		wantSentinel error
		wantNil      bool
	}{
		{
			"dead is terminal regardless of grace",
			config.ContainerStatusDead,
			false,
			ErrContainerTerminal,
			false,
		},
		{
			"dead is terminal even after grace",
			config.ContainerStatusDead,
			true,
			ErrContainerTerminal,
			false,
		},
		{
			"removing is terminal regardless of grace",
			config.ContainerStatusRemoving,
			false,
			ErrContainerTerminal,
			false,
		},
		{
			"removing is terminal even after grace",
			config.ContainerStatusRemoving,
			true,
			ErrContainerTerminal,
			false,
		},
		{"exited before grace is still booting", config.ContainerStatusExited, false, nil, true},
		{
			"exited after grace failed",
			config.ContainerStatusExited,
			true,
			ErrContainerExited,
			false,
		},
		{"created before grace is still booting", config.ContainerStatusCreated, false, nil, true},
		{
			"created after grace failed",
			config.ContainerStatusCreated,
			true,
			ErrContainerExited,
			false,
		},
		{"paused is not a terminal boot state", config.ContainerStatusPaused, true, nil, true},
		{
			"restarting is not a terminal boot state",
			config.ContainerStatusRestarting,
			false,
			nil,
			true,
		},
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
	terminal := failedBootSentinel(config.ContainerStatusRemoving, false)
	exited := failedBootSentinel(config.ContainerStatusExited, true)

	assert.ErrorIs(t, terminal, ErrContainerTerminal)
	assert.NotErrorIs(t, terminal, ErrContainerExited,
		"terminal states must not be reported as exited")
	assert.ErrorIs(t, exited, ErrContainerExited)
	assert.NotErrorIs(t, exited, ErrContainerTerminal,
		"exited-after-grace must not be reported as terminal")
}
