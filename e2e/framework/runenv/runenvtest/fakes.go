package runenvtest

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
)

// fakeRunner records commands and answers through handle.
type fakeRunner struct {
	mu     sync.Mutex
	Calls  []runenv.Command
	Handle func(runenv.Command) (runenv.CommandResult, error)
}

func (f *fakeRunner) Run(_ context.Context, c runenv.Command) (runenv.CommandResult, error) {
	f.mu.Lock()
	f.Calls = append(f.Calls, c)
	f.mu.Unlock()
	if f.Handle == nil {
		return runenv.CommandResult{}, nil
	}
	return f.Handle(c)
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

// memFiles is an in-memory FileStore keyed by host-native path.
type MemFiles struct {
	mu    sync.Mutex
	Data  map[string][]byte
	Modes map[string]fs.FileMode
	// writes lists the paths of successful WriteFile calls, in order.
	Writes []string
	// failWrite makes WriteFile fail for a path; failAppend makes AppendFile fail.
	FailWrite  map[string]error
	FailAppend error
}

func NewMemFiles() *MemFiles {
	return &MemFiles{
		Data:      map[string][]byte{},
		Modes:     map[string]fs.FileMode{},
		FailWrite: map[string]error{},
	}
}

// canon keys the fake FS by host-native path, so "/a/b" and `\a\b` agree on Windows.
func canon(name string) string { return filepath.Clean(name) }

func (m *MemFiles) ReadFile(name string) ([]byte, error) {
	name = canon(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.Data[name]
	if !ok {
		return nil, errNotExist("read", name)
	}
	return append([]byte(nil), data...), nil
}

func (m *MemFiles) WriteFile(name string, data []byte, perm fs.FileMode) error {
	name = canon(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.FailWrite[name]; err != nil {
		return err
	}
	m.Writes = append(m.Writes, name)
	m.Data[name], m.Modes[name] = append([]byte(nil), data...), perm
	return nil
}

func (m *MemFiles) AppendFile(name string, data []byte, perm fs.FileMode) error {
	name = canon(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailAppend != nil {
		return m.FailAppend
	}
	m.Data[name] = append(m.Data[name], data...)
	m.Modes[name] = perm
	return nil
}

func (m *MemFiles) MkdirAll(string, fs.FileMode) error { return nil }

func (m *MemFiles) Stat(name string) (fs.FileInfo, error) {
	name = canon(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.Data[name]
	if !ok {
		return nil, errNotExist("stat", name)
	}
	return memFileInfo{name: filepath.Base(name), size: int64(len(data))}, nil
}

func (m *MemFiles) Remove(name string) error {
	name = canon(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.Data[name]; !ok {
		return errNotExist("remove", name)
	}
	delete(m.Data, name)
	return nil
}

// RemoveAll deletes path and everything beneath it.
func (m *MemFiles) RemoveAll(path string) error {
	root := canon(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	for name := range m.Data {
		if name == root || strings.HasPrefix(name, root+string(filepath.Separator)) {
			delete(m.Data, name)
		}
	}
	return nil
}

func (m *MemFiles) Has(name string) bool {
	name = canon(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.Data[name]
	return ok
}

// text returns the file's content, or "" when it does not exist.
func (m *MemFiles) Text(name string) string {
	data, err := m.ReadFile(name)
	if err != nil {
		return ""
	}
	return string(data)
}

// under lists, sorted, the files strictly beneath dir.
func (m *MemFiles) Under(dir string) []string {
	prefix := canon(dir) + string(filepath.Separator)
	m.mu.Lock()
	defer m.mu.Unlock()
	var names []string
	for name := range m.Data {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// fakeClock reports a fixed elapsed time and never sleeps.
type FakeClock struct{ Step time.Duration }

func (c FakeClock) Now() time.Time                             { return time.Unix(1_700_000_000, 0) }
func (c FakeClock) Since(time.Time) time.Duration              { return c.Step }
func (c FakeClock) Sleep(context.Context, time.Duration) error { return nil }

type fakeDialer struct{}

func (fakeDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, io.EOF
}

type fakeDownloader struct{}

func (fakeDownloader) Download(context.Context, string, io.Writer, int64) error { return nil }

// Rig is Dependencies over fakes plus handles to the fakes.
type Rig struct {
	*runenv.Dependencies
	Runner *fakeRunner
	FS     *MemFiles
	Output *bytes.Buffer
}

func NewRig(goos string, env []string) *Rig {
	rig := &Rig{Runner: &fakeRunner{}, FS: NewMemFiles(), Output: &bytes.Buffer{}}
	rig.Dependencies = &runenv.Dependencies{
		Run:     rig.Runner,
		Files:   rig.FS,
		Clock:   FakeClock{Step: 3 * time.Second},
		Net:     fakeDialer{},
		HTTP:    fakeDownloader{},
		GOOS:    goos,
		Environ: func() []string { return env },
		Out:     rig.Output,
		Err:     rig.Output,
	}
	return rig
}

// Environment entries shared by the runner and docker-wsl tests.
const (
	EnvRunnerTemp         = "RUNNER_TEMP=/tmp/rt"
	EnvDockerContextEmpty = "DOCKER_CONTEXT="
)

// codesOf lists the Code of every StageError in err, including joined errors.
func CodesOf(err error) []runenv.Code {
	var codes []runenv.Code
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		if se, ok := e.(*runenv.StageError); ok {
			codes = append(codes, se.Code)
			return
		}
		if multi, ok := e.(interface{ Unwrap() []error }); ok {
			for _, inner := range multi.Unwrap() {
				walk(inner)
			}
		}
	}
	walk(err)
	return codes
}
