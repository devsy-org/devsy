package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/stretchr/testify/require"
)

// Shared literals for the docker-wsl tests.
const (
	testRunID      = "77-1-e2e"
	testAlias      = "devsy-ci-" + testRunID
	testStateFile  = "/tmp/rt/devsy-ci/docker-wsl-state.json"
	testFixture    = "/tmp/rt/devsy-ci/run-" + testRunID
	testUserConfig = "/home/runneradmin/.ssh/config"
	testGHEnv      = "/gh/env"
	testGHPath     = "/gh/path"
	testDaemonID   = "ABCD:EFGH:IJKL:MNOP"
	testPrivateKey = "PRIVATE-KEY-BYTES-SENTINEL"
	testVMIP       = "172.20.1.5"
	testBanner     = "SSH-2.0-OpenSSH_9.6\r\n"
)

// Command kinds and literals reused across the docker-wsl tests.
const (
	kindDockerInfoDead = "docker-info-dead"
	kindDockerInfoSSH  = "docker-info-ssh"
	kindWSLDockerInfo  = "wsl-docker-info"
	kindWSLUnknown     = "wsl-unknown"
	testExeEntry       = "docker/docker.exe"
	etcPasswd          = "/etc/passwd"
	testStateDir       = "/tmp/rt/devsy-ci"
	httpBanner         = "HTTP/1.1 400\r\n"
	partExit1          = "exit=1"
	cmdDockerWSL       = "docker-wsl"
	cmdSetup           = "setup"
	testZipURL         = "https://example.test/d.zip"
)

var (
	scriptWrapper = regexp.MustCompile(
		`printf %s ([A-Za-z0-9+/=]+) \| base64 -d > \$f && sh \$f ?([^;]*);`,
	)
	scriptKinds = map[string]string{
		preflightScript:           "wsl-preflight",
		installPackagesScript:     "wsl-install",
		stopInstallerDaemonScript: "wsl-stop-installer",
		prepareRuntimeScript:      "wsl-prepare",
		createUserScript:          "wsl-create-user",
		configureSSHDScript:       "wsl-configure-sshd",
		installKeyScript:          "wsl-install-key",
		removeUserScript:          "wsl-remove-user",
		removeFilesScript:         "wsl-remove-files",
		diagnosticsScript:         "wsl-diagnostics",
	}
)

// wslFiles is memFiles with tree-aware RemoveAll and write fault injection.
type wslFiles struct {
	*memFiles
	failWrite map[string]error
	writes    []string
}

func newWSLFiles() *wslFiles {
	return &wslFiles{memFiles: newMemFiles(), failWrite: map[string]error{}}
}

func (w *wslFiles) WriteFile(name string, data []byte, perm fs.FileMode) error {
	if err := w.failWrite[name]; err != nil {
		return err
	}
	w.mu.Lock()
	w.writes = append(w.writes, name)
	w.mu.Unlock()
	return w.memFiles.WriteFile(name, data, perm)
}

func (w *wslFiles) RemoveAll(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for name := range w.files {
		if name == path || strings.HasPrefix(name, path+"/") {
			delete(w.files, name)
		}
	}
	return nil
}

func (w *wslFiles) under(dir string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var names []string
	for name := range w.files {
		if strings.HasPrefix(name, dir+"/") {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func (w *wslFiles) text(name string) string {
	data, err := w.ReadFile(name)
	if err != nil {
		return ""
	}
	return string(data)
}

// wslHost is the fake hostPlatform.
type wslHost struct {
	lookErr  map[string]error
	startErr error
	started  [][]string
	onStart  func(args []string)
}

func (h *wslHost) LookPath(file string) (string, error) {
	if err := h.lookErr[file]; err != nil {
		return "", err
	}
	return "/fake/bin/" + file, nil
}

func (h *wslHost) StartDetached(_ string, args []string) error {
	if h.onStart != nil {
		h.onStart(args)
	}
	h.started = append(h.started, args)
	return h.startErr
}

// scriptedDialer answers by address: a banner to send, or "refuse".
type scriptedDialer struct {
	mu     sync.Mutex
	answer map[string]string
	dialed []string
}

func (s *scriptedDialer) DialContext(_ context.Context, _, addr string) (net.Conn, error) {
	s.mu.Lock()
	s.dialed = append(s.dialed, addr)
	reply, ok := s.answer[addr]
	s.mu.Unlock()
	if !ok || reply == "refuse" {
		return nil, errors.New("connection refused")
	}
	client, server := net.Pipe()
	go func() {
		defer func() { _ = server.Close() }()
		for _, chunk := range splitBanner(reply) {
			_, _ = server.Write([]byte(chunk))
		}
	}()
	return client, nil
}

// splitBanner sends the first two bytes on their own to force a partial read.
func splitBanner(reply string) []string {
	if len(reply) <= 2 {
		return nil
	}
	return []string{reply[:2], reply[2:]}
}

// stubDownloader serves canned bytes, optionally failing midway.
type stubDownloader struct {
	data    []byte
	err     error
	partial bool
	calls   int
}

func (s *stubDownloader) Download(_ context.Context, _ string, dst io.Writer, _ int64) error {
	s.calls++
	if s.partial {
		_, _ = dst.Write(s.data[:len(s.data)/2])
		return s.err
	}
	if s.err != nil {
		return s.err
	}
	_, err := dst.Write(s.data)
	return err
}

// scriptRunner is the fake Runner. Commands are classified into kinds so a
// test can override one behavior and still get a working default for the rest.
type scriptRunner struct {
	mu        sync.Mutex
	calls     []Command
	kinds     []string
	overrides map[string]func(context.Context, Command) (CommandResult, error)
	files     *wslFiles
	idSSH     string
}

func (r *scriptRunner) Run(ctx context.Context, c Command) (CommandResult, error) {
	kind := classifyCommand(c)
	r.mu.Lock()
	r.calls = append(r.calls, c)
	r.kinds = append(r.kinds, kind)
	override := r.overrides[kind]
	r.mu.Unlock()
	if override != nil {
		return override(ctx, c)
	}
	return r.defaultResult(kind, c), nil
}

func (r *scriptRunner) count(kind string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, k := range r.kinds {
		if k == kind {
			n++
		}
	}
	return n
}

func (r *scriptRunner) index(kind string) int { return slices.Index(r.kinds, kind) }

func (r *scriptRunner) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls, r.kinds = nil, nil
}

func classifyCommand(c Command) string {
	switch strings.ToLower(filepath.Base(c.Path)) {
	case toolWSL:
		return classifyWSLArgs(c.Args)
	case toolSSHKeygen:
		return "keygen"
	case toolICACLS:
		return classifyICACLS(c.Args)
	case toolSSH:
		if slices.Contains(c.Args, "-vv") {
			return "ssh-verbose"
		}
		return "ssh"
	case dockerExeName:
		return classifyDocker(c)
	}
	return "other:" + c.Path
}

func classifyICACLS(args []string) string {
	switch {
	case slices.Contains(args, "/save"):
		return "icacls-save"
	case slices.Contains(args, "/restore"):
		return "icacls-restore"
	case slices.Contains(args, "/inheritance:r"):
		return "icacls-restrict"
	}
	return "icacls-show"
}

func classifyDocker(c Command) string {
	switch {
	case slices.Contains(c.Args, "--version"):
		return "docker-version"
	case slices.Contains(c.Env, "DOCKER_HOST="+deadDockerHost):
		return kindDockerInfoDead
	}
	return kindDockerInfoSSH
}

func classifyWSLArgs(args []string) string {
	if len(args) > 0 && args[0] == "-l" {
		return "wsl-list"
	}
	idx := slices.Index(args, "--exec")
	if idx < 0 || idx+1 >= len(args) {
		return kindWSLUnknown
	}
	argv := args[idx+1:]
	switch argv[0] {
	case "docker":
		return kindWSLDockerInfo
	case "cat":
		return "wsl-cat-hostkey"
	case "hostname":
		return "wsl-hostname"
	}
	return classifyWSLScript(strings.Join(argv, " "))
}

func classifyWSLScript(joined string) string {
	m := scriptWrapper.FindStringSubmatch(joined)
	if m == nil {
		return kindWSLUnknown
	}
	body, _ := base64.StdEncoding.DecodeString(m[1])
	switch string(body) {
	case tcpListenScript:
		return "wsl-listener-" + m[2]
	case stopOwnedScript:
		return "wsl-stop-owned:" + strings.Fields(m[2])[0]
	}
	if kind, ok := scriptKinds[string(body)]; ok {
		return kind
	}
	return kindWSLUnknown
}

func testBlob(seed byte) []byte {
	blob := []byte("\x00\x00\x00\x0bssh-ed25519\x00\x00\x00\x20")
	return append(blob, bytes.Repeat([]byte{seed}, 32)...)
}

func testPublicKey(seed byte, comment string) string {
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(testBlob(seed)) + " " + comment
}

func utf16le(s string) string {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		out = binary.LittleEndian.AppendUint16(out, u)
	}
	return string(out)
}

// staticResults are the happy-path answers that need no per-call state.
var staticResults = map[string]CommandResult{
	"wsl-list": {Stdout: utf16le("Ubuntu-24.04\r\nDebian\r\n")},
	"wsl-preflight": {Stdout: "socket=absent\ndocker_installed=no\nsystemd=no\n" +
		"ci_user=absent\nleftovers=no\n"},
	"wsl-install": {
		Stdout: "noise\npackage docker-ce=5:29.9.0-1\npackage openssh-server=1:9.6\n",
	},
	"wsl-listener-" + dockerTCPPortHex: {ExitCode: 1},
	"wsl-cat-hostkey":                  {Stdout: testPublicKey(9, "root@host") + "\n"},
	"wsl-hostname":                     {Stdout: "127.0.0.1 " + testVMIP + " 172.17.0.1\n"},
	"docker-version":                   {Stdout: "Docker version 29.9.0, build abc\n"},
	kindDockerInfoDead:                 {ExitCode: 1, Stderr: "error during connect"},
}

func (r *scriptRunner) defaultResult(kind string, c Command) CommandResult {
	if res, ok := staticResults[kind]; ok {
		return res
	}
	switch kind {
	case "keygen":
		r.writeKeyPair(c.Args)
	case "icacls-show":
		return CommandResult{
			Stdout: c.Args[0] + ` HOST\runner:(R)` + "\n\nSuccessfully processed 1 files\n",
		}
	case kindWSLDockerInfo, kindDockerInfoSSH:
		return CommandResult{Stdout: r.daemonID(kind) + "\n"}
	}
	return CommandResult{}
}

func (r *scriptRunner) daemonID(kind string) string {
	if kind == kindDockerInfoSSH && r.idSSH != "" {
		return r.idSSH
	}
	return testDaemonID
}

func (r *scriptRunner) writeKeyPair(args []string) {
	key := args[slices.Index(args, "-f")+1]
	_ = r.files.memFiles.WriteFile(key, []byte(testPrivateKey), 0o600)
	_ = r.files.memFiles.WriteFile(key+".pub", []byte(testPublicKey(1, "devsy-ci")+"\n"), 0o644)
}

func zipOf(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(entries[name]))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validCLIZip(t *testing.T) []byte {
	return zipOf(t, map[string]string{
		testExeEntry:         "MZ-docker",
		"docker/dockerd.exe": "MZ-dockerd",
	})
}

// wslRig wires a dockerWSL over fakes.
type wslRig struct {
	t      *testing.T
	deps   *Dependencies
	files  *wslFiles
	runner *scriptRunner
	host   *wslHost
	dialer *scriptedDialer
	dl     *stubDownloader
	out    *bytes.Buffer
	env    []string
	zip    []byte
}

func newWSLRig(t *testing.T) *wslRig {
	t.Helper()
	files := newWSLFiles()
	rig := &wslRig{
		t:     t,
		files: files,
		host:  &wslHost{lookErr: map[string]error{}},
		out:   &bytes.Buffer{},
		dialer: &scriptedDialer{answer: map[string]string{
			"127.0.0.1:" + sshPort: testBanner,
		}},
		zip: validCLIZip(t),
		env: []string{
			envRunnerTemp, "USERNAME=runner", "USERPROFILE=/home/runneradmin",
			"GITHUB_ENV=" + testGHEnv, "GITHUB_PATH=" + testGHPath,
			"GITHUB_RUN_ID=77", "GITHUB_RUN_ATTEMPT=1", "GITHUB_JOB=e2e",
		},
	}
	rig.runner = &scriptRunner{
		files:     files,
		overrides: map[string]func(context.Context, Command) (CommandResult, error){},
	}
	rig.dl = &stubDownloader{data: rig.zip}
	rig.deps = &Dependencies{
		Run:     rig.runner,
		Files:   files,
		Clock:   fakeClock{step: time.Second},
		Net:     rig.dialer,
		HTTP:    rig.dl,
		GOOS:    goosWindows,
		Environ: func() []string { return rig.env },
		Out:     rig.out,
		Err:     rig.out,
	}
	return rig
}

// controller returns a fresh dockerWSL, as a new process would have.
func (r *wslRig) controller(opts ...func(*dockerWSLOptions)) *dockerWSL {
	r.t.Helper()
	o := dockerWSLOptions{Timeout: defaultSetupTimeout}
	for _, f := range opts {
		f(&o)
	}
	d, err := newDockerWSL(r.deps, r.host, o)
	require.NoError(r.t, err)
	d.cli = dockerCLISource{URL: "https://example.test/docker.zip", SHA256: sha256Hex(r.zip)}
	return d
}

func (r *wslRig) setup() error { return r.controller().setup(context.Background()) }

func (r *wslRig) cleanup() error { return r.controller().cleanup(context.Background()) }

func (r *wslRig) on(kind string, f func(context.Context, Command) (CommandResult, error)) {
	r.runner.overrides[kind] = f
}

func (r *wslRig) onResult(kind string, res CommandResult) {
	r.on(kind, func(context.Context, Command) (CommandResult, error) { return res, nil })
}

func (r *wslRig) state() *dockerWSLState {
	r.t.Helper()
	st, err := newStateStore(r.files, testStateFile).load()
	require.NoError(r.t, err)
	return st
}

func (r *wslRig) allArgs() string {
	var b strings.Builder
	for _, c := range r.runner.calls {
		fmt.Fprintln(&b, c.Path, strings.Join(c.Args, " "), strings.Join(c.Env, " "))
	}
	return b.String()
}

// errTest is a constant-style error for fault injection.
type errTest string

func (e errTest) Error() string { return string(e) }
