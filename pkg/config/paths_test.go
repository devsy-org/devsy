package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestReadDevContainerResultCommandSelectsNewestResultSelector(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "primary.json")
	fallback := filepath.Join(dir, "fallback.json")
	primarySelector := filepath.Join(dir, "primary.path")
	fallbackSelector := filepath.Join(dir, "fallback.path")
	command := readDevContainerResultCommand(primary, fallback, primarySelector, fallbackSelector)

	if err := os.WriteFile(primary, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallback, []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(primarySelector, []byte(primary), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallbackSelector, []byte(fallback), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(primarySelector, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fallbackSelector, time.Unix(2, 0), time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("sh", "-c", command).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "current" {
		t.Fatalf("result = %q, want current", output)
	}
}

func TestReadDevContainerResultCommandRequiresSelector(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "primary.json")
	fallback := filepath.Join(dir, "fallback.json")
	command := readDevContainerResultCommand(
		primary,
		fallback,
		filepath.Join(dir, "primary.path"),
		filepath.Join(dir, "fallback.path"),
	)
	if err := os.WriteFile(fallback, []byte("fallback"), 0o644); err != nil {
		t.Fatal(err)
	}

	if output, err := exec.Command("sh", "-c", command).CombinedOutput(); err == nil {
		t.Fatalf("result = %q, want missing-selector error", output)
	}
}

func TestReadDevContainerResultCommandSkipsMissingSelectedPrimary(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "primary.json")
	fallback := filepath.Join(dir, "fallback.json")
	primarySelector := filepath.Join(dir, "primary.path")
	fallbackSelector := filepath.Join(dir, "fallback.path")
	command := readDevContainerResultCommand(primary, fallback, primarySelector, fallbackSelector)

	if err := os.WriteFile(primarySelector, []byte(primary), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallback, []byte("current"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallbackSelector, []byte(fallback), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(primarySelector, time.Unix(2, 0), time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fallbackSelector, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}

	output, err := exec.Command("sh", "-c", command).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "current" {
		t.Fatalf("result = %q, want current", output)
	}
}
