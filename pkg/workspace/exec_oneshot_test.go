package workspace

import (
	"testing"
)

func TestExecOneShotOptions_ResolveTimeout_Clamp(t *testing.T) {
	opts := ExecOneShotOptions{
		TimeoutSeconds:    10000,
		TimeoutSecondsMax: 60,
	}
	clamped, wasClamped := opts.ResolveTimeout(5)
	if !wasClamped {
		t.Fatal("expected clamp=true")
	}
	if clamped.Seconds() != 60 {
		t.Fatalf("expected 60s, got %s", clamped)
	}
}

func TestExecOneShotOptions_ResolveTimeout_Default(t *testing.T) {
	opts := ExecOneShotOptions{TimeoutSecondsMax: 600}
	clamped, wasClamped := opts.ResolveTimeout(300)
	if wasClamped {
		t.Fatal("expected clamp=false")
	}
	if clamped.Seconds() != 300 {
		t.Fatalf("expected 300s, got %s", clamped)
	}
}

func TestExecOneShotOptions_ResolveTimeout_CallerExplicit(t *testing.T) {
	opts := ExecOneShotOptions{
		TimeoutSeconds:    120,
		TimeoutSecondsMax: 600,
	}
	clamped, wasClamped := opts.ResolveTimeout(300)
	if wasClamped {
		t.Fatal("expected clamp=false")
	}
	if clamped.Seconds() != 120 {
		t.Fatalf("expected 120s, got %s", clamped)
	}
}
