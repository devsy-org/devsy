package provider

import (
	"log/slog"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
)

func TestBaseEnvironmentUsesEffectiveJSONLogging(t *testing.T) {
	log.InitTestObserved(t, slog.LevelDebug)
	env := GetBaseEnvironment("test-context", "test-provider")
	if got := env[config.EnvLogLevel]; got != log.LevelString() {
		t.Fatalf("log level = %q, want %q", got, log.LevelString())
	}
	if got := env["DEVSY_LOG_FORMAT"]; got != "json" {
		t.Fatalf("log format = %q, want json", got)
	}
	if _, ok := env["DEVSY_DEBUG"]; ok {
		t.Fatal("obsolete debug variable exported")
	}
}
