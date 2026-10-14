package main

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Shared test literals.
const (
	buildkitLabel         = "docker-internal-buildkit"
	fixtureValid4         = "valid_4of4.json"
	msgNoExecuted         = "no executed It spec"
	cmdPodman             = "podman"
	envGHUser             = "GH_USERNAME=octo"
	envRunnerTemp         = "RUNNER_TEMP=/tmp/rt"
	envDockerContextEmpty = "DOCKER_CONTEXT="
	hiddenDriveEntry      = `=C:=C:\x`
)

// fakeRunner records commands and answers through handle.
type fakeRunner struct {
	mu     sync.Mutex
	calls  []Command
	handle func(Command) (CommandResult, error)
}

func (f *fakeRunner) Run(_ context.Context, c Command) (CommandResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	if f.handle == nil {
		return CommandResult{}, nil
	}
	return f.handle(c)
}

type memFileInfo struct {
	name string
	size int64
}

func (i memFileInfo) Name() string       { return i.name }
func (i memFileInfo) Size() int64        { return i.size }
func (i memFileInfo) Mode() fs.FileMode  { return 0o644 }
func (i memFileInfo) ModTime() time.Time { return time.Time{} }
func (i memFileInfo) IsDir() bool        { return false }
func (i memFileInfo) Sys() any           { return nil }

func errNotExist(op, name string) error {
	return &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
}

// memFiles is an in-memory FileStore.
type memFiles struct {
	mu    sync.Mutex
	files map[string][]byte
	modes map[string]fs.FileMode
	// failAppend makes AppendFile fail, for fault injection.
	failAppend error
}

func newMemFiles() *memFiles {
	return &memFiles{files: map[string][]byte{}, modes: map[string]fs.FileMode{}}
}

// canon keys the fake FS by host-native path, so "/a/b" and `\a\b` agree on Windows.
func canon(name string) string { return filepath.Clean(name) }

func (m *memFiles) ReadFile(name string) ([]byte, error) {
	name = canon(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[name]
	if !ok {
		return nil, errNotExist("read", name)
	}
	return append([]byte(nil), data...), nil
}

func (m *memFiles) WriteFile(name string, data []byte, perm fs.FileMode) error {
	name = canon(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[name], m.modes[name] = append([]byte(nil), data...), perm
	return nil
}

func (m *memFiles) AppendFile(name string, data []byte, perm fs.FileMode) error {
	name = canon(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failAppend != nil {
		return m.failAppend
	}
	m.files[name] = append(m.files[name], data...)
	m.modes[name] = perm
	return nil
}

func (m *memFiles) MkdirAll(string, fs.FileMode) error { return nil }

func (m *memFiles) Stat(name string) (fs.FileInfo, error) {
	name = canon(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[name]
	if !ok {
		return nil, errNotExist("stat", name)
	}
	return memFileInfo{name: filepath.Base(name), size: int64(len(data))}, nil
}

func (m *memFiles) Remove(name string) error {
	name = canon(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[name]; !ok {
		return errNotExist("remove", name)
	}
	delete(m.files, name)
	return nil
}

func (m *memFiles) RemoveAll(path string) error {
	path = canon(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, path)
	return nil
}

func (m *memFiles) has(name string) bool {
	name = canon(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.files[name]
	return ok
}

// fakeClock reports a fixed elapsed time and never sleeps.
type fakeClock struct{ step time.Duration }

func (c fakeClock) Now() time.Time                             { return time.Unix(1_700_000_000, 0) }
func (c fakeClock) Since(time.Time) time.Duration              { return c.step }
func (c fakeClock) Sleep(context.Context, time.Duration) error { return nil }

type fakeDialer struct{}

func (fakeDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, io.EOF
}

type fakeDownloader struct{}

func (fakeDownloader) Download(context.Context, string, io.Writer, int64) error { return nil }

// testRig is Dependencies over fakes plus handles to the fakes.
type testRig struct {
	*Dependencies
	runner *fakeRunner
	files  *memFiles
	out    *bytes.Buffer
}

func newTestRig(goos string, env []string) *testRig {
	rig := &testRig{runner: &fakeRunner{}, files: newMemFiles(), out: &bytes.Buffer{}}
	rig.Dependencies = &Dependencies{
		Run:     rig.runner,
		Files:   rig.files,
		Clock:   fakeClock{step: 3 * time.Second},
		Net:     fakeDialer{},
		HTTP:    fakeDownloader{},
		GOOS:    goos,
		Environ: func() []string { return env },
		Out:     rig.out,
		Err:     rig.out,
	}
	return rig
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return data
}
