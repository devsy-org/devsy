package provider

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
)

func useTempDevsyHome(t *testing.T) {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	config.SetPathManager(config.NewPathManager())
}

func TestResolveInitState_NeverAttempted(t *testing.T) {
	useTempDevsyHome(t)

	got, err := ResolveInitState("default", "docker", &config.ProviderConfig{})
	if err != nil {
		t.Fatalf("ResolveInitState: %v", err)
	}
	if got != InitStateNotInitialized {
		t.Fatalf("got %q, want %q", got, InitStateNotInitialized)
	}
}

func TestResolveInitState_NilState(t *testing.T) {
	useTempDevsyHome(t)

	got, err := ResolveInitState("default", "docker", nil)
	if err != nil {
		t.Fatalf("ResolveInitState: %v", err)
	}
	if got != InitStateNotInitialized {
		t.Fatalf("got %q, want %q", got, InitStateNotInitialized)
	}
}

func TestResolveInitState_Initialized(t *testing.T) {
	useTempDevsyHome(t)

	got, err := ResolveInitState("default", "docker", &config.ProviderConfig{Initialized: true})
	if err != nil {
		t.Fatalf("ResolveInitState: %v", err)
	}
	if got != InitStateInitialized {
		t.Fatalf("got %q, want %q", got, InitStateInitialized)
	}
}

func TestResolveInitState_LiveLockHeldMeansInitializing(t *testing.T) {
	useTempDevsyHome(t)

	lock, err := GetProviderInitLock("default", "docker")
	if err != nil {
		t.Fatalf("GetProviderInitLock: %v", err)
	}
	locked, err := lock.TryLock()
	if err != nil || !locked {
		t.Fatalf("TryLock: locked=%v err=%v", locked, err)
	}
	defer func() { _ = lock.Unlock() }()

	got, err := ResolveInitState("default", "docker", &config.ProviderConfig{InitAttempted: true})
	if err != nil {
		t.Fatalf("ResolveInitState: %v", err)
	}
	if got != InitStateInitializing {
		t.Fatalf("got %q, want %q", got, InitStateInitializing)
	}
}

func TestResolveInitState_FreeLockWithAttemptMeansFailed(t *testing.T) {
	useTempDevsyHome(t)

	// Simulate a crash mid-init: InitAttempted was persisted before the
	// process died, but nothing holds the lock anymore since the OS released
	// it when the process exited.
	got, err := ResolveInitState("default", "docker", &config.ProviderConfig{InitAttempted: true})
	if err != nil {
		t.Fatalf("ResolveInitState: %v", err)
	}
	if got != InitStateFailed {
		t.Fatalf("got %q, want %q", got, InitStateFailed)
	}
}
