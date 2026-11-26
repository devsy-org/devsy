package main

import (
	"errors"
	"io"
	"os/exec"
)

type suiteConfig struct {
	label         string
	expectedCount int
	providerMode  string
	repositoryDir string
	tempDir       string
	testTimeout   string
}

type commandRunner func(*exec.Cmd) error

type commandOutput struct {
	stdout io.Writer
	stderr io.Writer
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
		return exitErr.ExitCode()
	}
	return 1
}
