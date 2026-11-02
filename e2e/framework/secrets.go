package framework

// UseFileSecretsBackend forces the file backend so tests do not depend on an OS
// keyring (unavailable in CI); env is restored on cleanup.
func UseFileSecretsBackend() {
	SetEnvForSpec("DEVSY_SECRETS_BACKEND", "file")
	SetEnvForSpec("DEVSY_SECRETS_PASSPHRASE", "e2e-passphrase")
}
