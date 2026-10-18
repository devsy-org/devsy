package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// fakeProcesses stands in for the go and git subprocesses so tests run
// without a toolchain or network. go list output is keyed by "GOOS/GOARCH";
// unlisted targets produce no output. The detector writes detectorOut to its
// -depsOut path and records the stdin it was given.
type fakeProcesses struct {
	listOut     map[string]string
	listErr     map[string]error
	detectorOut string
	detectorIn  []byte
}

func (f *fakeProcesses) run(cmd *exec.Cmd) error {
	switch {
	case cmd.Args[0] == "git":
		return nil
	case cmd.Args[1] == "list":
		key := envValue(cmd.Env, "GOOS") + "/" + envValue(cmd.Env, "GOARCH")
		if err := f.listErr[key]; err != nil {
			return err
		}
		_, err := io.WriteString(cmd.Stdout, f.listOut[key])
		return err
	default:
		in, err := io.ReadAll(cmd.Stdin)
		if err != nil {
			return err
		}
		f.detectorIn = in
		out := argValue(cmd.Args, "-depsOut")
		if out == "" {
			return fmt.Errorf("detector invoked without -depsOut")
		}
		return os.WriteFile(out, []byte(f.detectorOut), 0o644)
	}
}

// envValue returns the last value for key, matching exec's override order.
func envValue(env []string, key string) string {
	val := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			val = v
		}
	}
	return val
}

// argValue returns the argument following flag, or "" if absent.
func argValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

// lines joins its arguments with newlines and appends a trailing newline.
func lines(parts ...string) string {
	return strings.Join(parts, "\n") + "\n"
}

// fatalOnErr fails the test when err is non-nil.
func fatalOnErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
