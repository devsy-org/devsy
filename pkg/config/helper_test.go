package config

import "testing"

func TestHelperImage(t *testing.T) {
	t.Run("explicit wins over env and default", func(t *testing.T) {
		t.Setenv(EnvHelperImage, "env-image")
		if got := HelperImage("explicit-image"); got != "explicit-image" {
			t.Fatalf("got %q, want explicit-image", got)
		}
	})

	t.Run("env used when no explicit value", func(t *testing.T) {
		t.Setenv(EnvHelperImage, "env-image")
		if got := HelperImage(""); got != "env-image" {
			t.Fatalf("got %q, want env-image", got)
		}
	})

	t.Run("default when nothing set", func(t *testing.T) {
		t.Setenv(EnvHelperImage, "")
		if got := HelperImage(""); got != DefaultHelperImage {
			t.Fatalf("got %q, want %q", got, DefaultHelperImage)
		}
	})
}
