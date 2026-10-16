// Package dockerwsl provisions and tears down the Docker daemon the Windows
// E2E runners use: dockerd inside WSL2, reached from the host over SSH.
//
// Setup is a sequence of stages whose progress and owned resources are
// persisted to a state file after each step. Cleanup reads that file, so it
// can finish a job whatever stage setup failed in, and it refuses state that
// names paths outside the job's own fixture directory.
package dockerwsl
