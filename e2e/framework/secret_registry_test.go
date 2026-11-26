package framework

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSentinelFileEncodesAndDeduplicates(t *testing.T) {
	restoreRegisteredSecrets(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "sentinels")
	t.Setenv("DEVSY_E2E_SENTINEL_FILE", path)
	RegisterSecret("")
	unique := dir + "/registry-value"
	var workers sync.WaitGroup
	for i := range 24 {
		workers.Go(func() {
			RegisterSecret(fmt.Sprintf("%s-%d", unique, i%6))
			RegisterSecret(unique + "\n\"quoted\"")
		})
	}
	workers.Wait()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile("sentinels")
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	require.Len(t, lines, 7)
	seen := make(map[string]int)
	for _, line := range lines {
		var value string
		require.NoError(t, json.Unmarshal([]byte(line), &value))
		seen[value]++
	}
	require.Equal(t, 1, seen[unique+"\n\"quoted\""])
	for i := range 6 {
		require.Equal(t, 1, seen[fmt.Sprintf("%s-%d", unique, i)])
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestRegisteredSecretAcrossLines(t *testing.T) {
	restoreRegisteredSecrets(t)
	value := "multi-fixture\nsecond-part"
	RegisterSecret(value)
	t.Setenv("DEVSY_CAPTURE_CHILD", "registered-multiline")
	executable, err := os.Executable()
	require.NoError(t, err)
	f := &Framework{DevsyBinDir: filepath.Dir(executable), DevsyBinName: filepath.Base(executable)}
	out, err := os.CreateTemp(t.TempDir(), "stdout")
	require.NoError(t, err)
	defer func() { _ = out.Close() }()
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	require.NoError(t, err)
	defer func() { _ = stderr.Close() }()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = out, stderr
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	raw, rawErr, err := f.ExecCommandCapture(context.Background(), []string{captureChildArg})
	require.NoError(t, err)
	require.Contains(t, raw, value)
	require.Contains(t, rawErr, value)
	for _, file := range []*os.File{out, stderr} {
		data, readErr := os.ReadFile(file.Name())
		require.NoError(t, readErr)
		require.NotContains(t, string(data), "multi-fixture")
		require.NotContains(t, string(data), "second-part")
		require.Contains(t, string(data), "public detail")
	}
}

func TestRegisteredSecretMasksLiveCommands(t *testing.T) {
	restoreRegisteredSecrets(t)
	const value = "registered-fixture-value"
	RegisterSecret(value)
	t.Setenv("DEVSY_CAPTURE_CHILD", "registered")
	executable, err := os.Executable()
	require.NoError(t, err)
	f := &Framework{DevsyBinDir: filepath.Dir(executable), DevsyBinName: filepath.Base(executable)}
	for _, capture := range []bool{false, true} {
		t.Run(map[bool]string{false: "output", true: "capture"}[capture], func(t *testing.T) {
			out, err := os.CreateTemp(t.TempDir(), "stdout")
			require.NoError(t, err)
			defer func() { _ = out.Close() }()
			stderr, err := os.CreateTemp(t.TempDir(), "stderr")
			require.NoError(t, err)
			defer func() { _ = stderr.Close() }()
			oldOut, oldErr := os.Stdout, os.Stderr
			os.Stdout, os.Stderr = out, stderr
			defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
			var raw, rawErr string
			if capture {
				raw, rawErr, err = f.ExecCommandCapture(
					context.Background(),
					[]string{captureChildArg},
				)
				require.Contains(t, rawErr, value)
			} else {
				raw, err = f.ExecCommandOutput(context.Background(), []string{captureChildArg})
			}
			require.NoError(t, err)
			require.Contains(t, raw, value)
			for _, file := range []*os.File{out, stderr} {
				data, readErr := os.ReadFile(file.Name())
				require.NoError(t, readErr)
				require.Contains(t, string(data), "public detail")
				require.NotContains(t, string(data), value)
			}
		})
	}
}

func restoreRegisteredSecrets(t *testing.T) {
	t.Helper()
	registeredSecrets.Lock()
	saved := maps.Clone(registeredSecrets.values)
	registeredSecrets.Unlock()
	t.Cleanup(func() {
		registeredSecrets.Lock()
		defer registeredSecrets.Unlock()
		registeredSecrets.values = saved
	})
}
