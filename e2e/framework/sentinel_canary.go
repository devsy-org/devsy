package framework

import "os"

const sentinelCanary = "devsy-e2e-sentinel-canary-b03c74e8a6f1"

func init() {
	if os.Getenv("DEVSY_E2E_SENTINEL_FILE") != "" {
		RegisterSecret(sentinelCanary)
	}
}
