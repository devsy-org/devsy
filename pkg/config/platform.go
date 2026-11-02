package config

import "os"

// IsPlatformRunner reports whether the CLI runs inside a Platform runner pod; the new env var wins when set.
func IsPlatformRunner() bool {
	if value := os.Getenv(EnvPlatformRunner); value != "" {
		return value == BoolTrue
	}
	return os.Getenv(EnvPlatformRunnerLegacy) == BoolTrue
}
