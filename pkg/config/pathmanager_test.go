package config

import (
	"testing"
)

func TestDefaultPathManagerSingleton(t *testing.T) {
	ResetPathManager()
	t.Cleanup(ResetPathManager)

	pm1 := DefaultPathManager()
	pm2 := DefaultPathManager()

	if pm1 != pm2 {
		t.Error("DefaultPathManager returned different instances")
	}
}

func TestSetPathManagerOverride(t *testing.T) {
	ResetPathManager()
	t.Cleanup(ResetPathManager)

	original := DefaultPathManager()

	custom := NewPathManager()
	SetPathManager(custom)

	got := DefaultPathManager()
	if got != custom {
		t.Error("SetPathManager did not override the singleton")
	}
	if got == original {
		t.Error("SetPathManager did not replace the original instance")
	}
}

func TestResetPathManager(t *testing.T) {
	ResetPathManager()
	t.Cleanup(ResetPathManager)

	pm1 := DefaultPathManager()
	ResetPathManager()
	pm2 := DefaultPathManager()

	if pm1 == pm2 {
		t.Error("ResetPathManager did not clear the singleton — same instance returned")
	}
}

func TestNewPathManagerReturnsNewInstance(t *testing.T) {
	pm1 := NewPathManager()
	pm2 := NewPathManager()

	if pm1 == pm2 {
		t.Error("NewPathManager returned the same instance twice")
	}
}
