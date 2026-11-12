package config

import "os"

// IsPlatformRunner reports whether the CLI runs inside a Platform runner pod; the new env var wins when set.
func IsPlatformRunner() bool {
	return boolEnvWithLegacy(EnvPlatformRunner, EnvPlatformRunnerLegacy)
}

// IsPlatformEnabled reports whether the platform command tree is exposed; the new env var wins when set.
func IsPlatformEnabled() bool {
	return boolEnvWithLegacy(EnvPlatformEnabled, EnvPlatformEnabledLegacy)
}

func boolEnvWithLegacy(name, legacyName string) bool {
	if value := os.Getenv(name); value != "" {
		return value == BoolTrue
	}
	return os.Getenv(legacyName) == BoolTrue
}
