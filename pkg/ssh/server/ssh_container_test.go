package server

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const (
	tokenBase     = "TOKEN=base"
	tokenAttached = "TOKEN=attached"
	tokenExplicit = "TOKEN=explicit"
)

func TestReadSessionSecretEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "SUPERFOO"), []byte("sentinel-value"), 0o600,
	); err != nil {
		t.Fatal("write secret fixture")
	}

	got, err := readSessionSecretEnvironment(dir)
	if err != nil {
		t.Fatal("read session secret environment")
	}
	if len(got) != 1 || got[0] != "SUPERFOO=sentinel-value" {
		t.Error("session secret environment was not reconstructed")
	}
}

func TestMergeSessionEnvironmentPrecedence(t *testing.T) {
	tests := []struct {
		name             string
		base             []string
		attached         []string
		sessionOverrides []string
		want             []string
	}{
		{
			name:     "attached overrides base",
			base:     []string{"PATH=/usr/bin", tokenBase},
			attached: []string{tokenAttached},
			want:     []string{"PATH=/usr/bin", tokenAttached},
		},
		{
			name:             "session overrides attached",
			base:             []string{tokenBase},
			attached:         []string{tokenAttached},
			sessionOverrides: []string{tokenExplicit},
			want:             []string{tokenExplicit},
		},
		{
			name:             "three levels preserve unrelated variables",
			base:             []string{"A=base", tokenBase},
			attached:         []string{tokenAttached, "B=secret"},
			sessionOverrides: []string{tokenExplicit, "C=session"},
			want:             []string{"A=base", tokenExplicit, "B=secret", "C=session"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := mergeSessionEnvironment(test.base, test.attached, test.sessionOverrides)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("merged environment = %q, want %q", got, test.want)
			}
		})
	}
}
