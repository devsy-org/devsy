package main

import (
	"slices"
	"testing"
)

func TestSuiteSentinelPathForwarded(t *testing.T) {
	t.Setenv("DEVSY_E2E_SENTINEL_FILE", "/tmp/sentinel file")
	got := suiteSpecificEnvironment(suiteConfig{})
	if !slices.Contains(got, "DEVSY_E2E_SENTINEL_FILE=/tmp/sentinel file") {
		t.Fatal("sentinel path not forwarded")
	}
}
