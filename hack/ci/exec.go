package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// commandSpec describes one external command.
type commandSpec struct {
	Name   string
	Args   []string
	Dir    string
	Env    []string // appended to the current environment
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// commandRunner runs external commands. Tests substitute a fake so no real
// process, sudo or package manager is touched.
type commandRunner interface {
	Run(ctx context.Context, spec commandSpec) error
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, spec commandSpec) error {
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = append(os.Environ(), spec.Env...)
	cmd.Stdin = spec.Stdin
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", spec.Name, err)
	}
	return nil
}
