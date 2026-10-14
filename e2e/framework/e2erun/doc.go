// Package e2erun runs a compiled E2E test binary for one Ginkgo label under a
// runtime profile, then verifies the JSON report it produced.
//
// A profile fixes how the suite is launched (privileges, container runtime,
// environment). The docker-wsl-ssh profile first requires proof, through a
// SetupVerifier, that its runtime was fully provisioned.
package e2erun
