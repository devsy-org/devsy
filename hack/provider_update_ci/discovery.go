package main

import (
	"fmt"
	"io"
	"os/exec"
)

func providerTestCommand(selection string) *exec.Cmd {
	args := []string{"test", "-race", "-json", "-count=1", "-timeout", windowsTestTimeout}
	args = append(args, providerUpdatePackages...)
	args = append(args, selection, providerUpdateRunPattern)
	return exec.Command("go", args...)
}

func discoverProviderTests(log, stdout, stderr io.Writer) (map[testID]struct{}, error) {
	capture := newOutputCapture(log, stdout, stderr)
	command := providerTestCommand("-list")
	command.Stdout = capture.stdoutWriter()
	command.Stderr = capture.stderrWriter()
	err := command.Run()
	capture.finish()
	if err != nil {
		return nil, fmt.Errorf("discover provider update tests: %w", err)
	}
	if err := capture.err(); err != nil {
		return nil, fmt.Errorf("capture provider update test discovery: %w", err)
	}
	expected := capture.listedTests()
	packages := make(map[string]bool)
	for id := range expected {
		packages[id.packagePath] = true
	}
	for _, name := range providerUpdatePackages {
		if !packages[name] {
			return nil, fmt.Errorf("no provider update tests discovered in %s", name)
		}
	}
	return expected, nil
}
