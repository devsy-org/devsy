package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

const (
	actionRemoveDocker     = "remove-docker"
	actionInsecureRegistry = "insecure-registry"

	defaultHostsFile  = "/etc/hosts"
	defaultDaemonFile = "/etc/docker/daemon.json"
	dockerHostName    = "host.docker.internal"
	dockerHostLine    = "127.0.0.1 " + dockerHostName
	snapshotRegistry  = dockerHostName + ":15500"
	insecureKey       = "insecure-registries"
	dockerReadyWait   = 30 * time.Second
	dockerPollEvery   = time.Second

	podmanDownloadTimeout = 5 * time.Minute
	podmanPackageDir      = "podman-linux-amd64"
	uidmapTool            = "newuidmap"
	sudoCmd               = "sudo"
	setupDirPerm          = 0o755
	setupOwnerDirBits     = 0o700
	setupDownloadPerm     = 0o644
	partialSuffix         = ".partial"
)

var (
	errUnknownAction    = errors.New("unknown host setup action")
	errNotLinux         = errors.New("host setup runs only on linux")
	errDockerNotReady   = errors.New("docker did not become ready")
	errInvalidDaemon    = errors.New("invalid docker daemon config")
	errEscapesDest      = errors.New("archive entry escapes the extract directory")
	errUnsupportedEntry = errors.New("unsupported archive entry type")
)

// removeDockerCommands are run in order, each through sudo.
var removeDockerCommands = [][]string{
	{"systemctl", "stop", "docker.socket", "docker.service"},
	{
		"apt-get",
		"remove",
		"-y",
		"docker-ce",
		"docker-ce-cli",
		"containerd.io",
		"docker-buildx-plugin",
		"docker-compose-plugin",
	},
	{"rm", "-rf", "/var/lib/docker", "/var/lib/containerd", "/usr/bin/docker", "/usr/bin/dockerd"},
}

// hostSetup applies the host_setup actions of a suite on this machine.
type hostSetup struct {
	runner     commandRunner
	goos       string
	hostsFile  string
	daemonFile string
	stdout     io.Writer
	stderr     io.Writer
	now        func() time.Time
	sleep      func(time.Duration)
}

func newHostSetup(stdout, stderr io.Writer) *hostSetup {
	return &hostSetup{
		runner:     execRunner{},
		goos:       runtime.GOOS,
		hostsFile:  defaultHostsFile,
		daemonFile: defaultDaemonFile,
		stdout:     stdout,
		stderr:     stderr,
		now:        time.Now,
		sleep:      time.Sleep,
	}
}

// apply runs the named actions in order. Unknown names and non-Linux hosts
// are rejected before any action runs.
func (h *hostSetup) apply(ctx context.Context, actions []string) error {
	if len(actions) == 0 {
		return nil
	}
	steps := make([]func(context.Context) error, 0, len(actions))
	for _, name := range actions {
		step := h.step(name)
		if step == nil {
			return fmt.Errorf("%w: %q", errUnknownAction, name)
		}
		steps = append(steps, step)
	}
	if h.goos != osLinux {
		return fmt.Errorf("%w (GOOS is %s)", errNotLinux, h.goos)
	}
	for i, step := range steps {
		if err := step(ctx); err != nil {
			return fmt.Errorf("host setup %s: %w", actions[i], err)
		}
	}
	return nil
}

func (h *hostSetup) step(name string) func(context.Context) error {
	switch name {
	case actionRemoveDocker:
		return h.removeDocker
	case actionInsecureRegistry:
		return h.configureRegistry
	default:
		return nil
	}
}

func (h *hostSetup) sudo(ctx context.Context, args ...string) error {
	return h.runner.Run(
		ctx,
		commandSpec{Name: sudoCmd, Args: args, Stdout: h.stdout, Stderr: h.stderr},
	)
}

func (h *hostSetup) removeDocker(ctx context.Context) error {
	for _, args := range removeDockerCommands {
		if err := h.sudo(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

func (h *hostSetup) configureRegistry(ctx context.Context) error {
	if err := h.addHostsEntry(ctx); err != nil {
		return err
	}
	if err := h.writeDaemonConfig(ctx); err != nil {
		return err
	}
	if err := h.sudo(ctx, "systemctl", "restart", "docker"); err != nil {
		return fmt.Errorf("restart docker: %w", err)
	}
	return h.waitForDocker(ctx)
}

func (h *hostSetup) addHostsEntry(ctx context.Context) error {
	hosts, err := os.ReadFile(h.hostsFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", h.hostsFile, err)
	}
	if hostsMapsName(hosts, dockerHostName) {
		return nil
	}
	return h.runner.Run(ctx, commandSpec{
		Name:   sudoCmd,
		Args:   []string{"tee", "-a", h.hostsFile},
		Stdin:  strings.NewReader(dockerHostLine + "\n"),
		Stdout: io.Discard,
		Stderr: h.stderr,
	})
}

func (h *hostSetup) writeDaemonConfig(ctx context.Context) error {
	existing, err := readOptionalFile(h.daemonFile)
	if err != nil {
		return err
	}
	merged, err := mergeInsecureRegistry(existing, snapshotRegistry)
	if err != nil {
		return fmt.Errorf("merge %s: %w", h.daemonFile, err)
	}
	if err := h.sudo(ctx, "mkdir", "-p", filepath.Dir(h.daemonFile)); err != nil {
		return fmt.Errorf("create docker config directory: %w", err)
	}
	return h.runner.Run(ctx, commandSpec{
		Name:   sudoCmd,
		Args:   []string{"tee", h.daemonFile},
		Stdin:  bytes.NewReader(merged),
		Stdout: io.Discard,
		Stderr: h.stderr,
	})
}

// waitForDocker polls docker info once per dockerPollEvery until it succeeds
// or dockerReadyWait has passed.
func (h *hostSetup) waitForDocker(ctx context.Context) error {
	deadline := h.now().Add(dockerReadyWait)
	for {
		err := h.runner.Run(ctx, commandSpec{
			Name:   "docker",
			Args:   []string{"info"},
			Stdout: io.Discard,
			Stderr: io.Discard,
		})
		if err == nil {
			return nil
		}
		if !h.now().Before(deadline) {
			return fmt.Errorf("%w within %s: %w", errDockerNotReady, dockerReadyWait, err)
		}
		h.sleep(dockerPollEvery)
	}
}

// hostsMapsName reports whether an /etc/hosts style file maps name on an
// uncommented line.
func hostsMapsName(content []byte, name string) bool {
	for line := range strings.SplitSeq(string(content), "\n") {
		line, _, _ = strings.Cut(line, "#")
		fields := strings.Fields(line)
		if len(fields) > 1 && slices.Contains(fields[1:], name) {
			return true
		}
	}
	return false
}

// readOptionalFile returns nil when the file does not exist.
func readOptionalFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

// mergeInsecureRegistry adds registry to the insecure-registries list of a
// daemon.json document. Other keys are kept; the list is sorted and unique.
func mergeInsecureRegistry(existing []byte, registry string) ([]byte, error) {
	cfg, err := decodeDaemonConfig(existing)
	if err != nil {
		return nil, err
	}
	registries, err := insecureRegistriesOf(cfg)
	if err != nil {
		return nil, err
	}
	registries = append(registries, registry)
	slices.Sort(registries)
	cfg[insecureKey] = slices.Compact(registries)
	return encodeDaemonConfig(cfg)
}

// decodeDaemonConfig parses a daemon.json document; empty input is an empty object.
func decodeDaemonConfig(data []byte) (map[string]any, error) {
	cfg := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return cfg, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidDaemon, err)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return cfg, nil
}

func insecureRegistriesOf(cfg map[string]any) ([]string, error) {
	raw, ok := cfg[insecureKey]
	if !ok || raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a list", errInvalidDaemon, insecureKey)
	}
	registries := make([]string, 0, len(items))
	for _, item := range items {
		name, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %s has a non-string entry", errInvalidDaemon, insecureKey)
		}
		registries = append(registries, name)
	}
	return registries, nil
}

// encodeDaemonConfig writes indented JSON with sorted keys and a trailing newline.
func encodeDaemonConfig(cfg map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return nil, fmt.Errorf("encode docker daemon config: %w", err)
	}
	return buf.Bytes(), nil
}

// podmanInstall installs the podman-static archive system-wide.
type podmanInstall struct {
	archive    string
	url        string
	sha256Hex  string
	extractDir string
	client     *http.Client
	runner     commandRunner
	lookPath   func(string) (string, error)
	stdout     io.Writer
	stderr     io.Writer
}

// checksumMismatchError reports an archive whose SHA-256 differs from the pinned value.
type checksumMismatchError struct {
	expected string
	actual   string
}

func (e *checksumMismatchError) Error() string {
	return fmt.Sprintf(
		"SHA256 mismatch for podman-static archive! Expected: %s, Got: %s",
		e.expected,
		e.actual,
	)
}

func (p *podmanInstall) install(ctx context.Context) error {
	if err := p.ensureArchive(ctx); err != nil {
		return err
	}
	if err := verifySHA256(p.archive, p.sha256Hex); err != nil {
		return err
	}
	if err := extractTarGz(p.archive, p.extractDir); err != nil {
		return fmt.Errorf("extract %s: %w", p.archive, err)
	}
	src := filepath.Join(p.extractDir, podmanPackageDir)
	if err := p.sudo(
		ctx,
		"cp",
		"-r",
		filepath.Join(src, "usr"),
		filepath.Join(src, "etc"),
		"/",
	); err != nil {
		return fmt.Errorf("install podman files: %w", err)
	}
	return p.ensureUIDMap(ctx)
}

func (p *podmanInstall) sudo(ctx context.Context, args ...string) error {
	return p.runner.Run(
		ctx,
		commandSpec{Name: sudoCmd, Args: args, Stdout: p.stdout, Stderr: p.stderr},
	)
}

// ensureUIDMap installs uidmap only when newuidmap is missing.
func (p *podmanInstall) ensureUIDMap(ctx context.Context) error {
	if _, err := p.lookPath(uidmapTool); err == nil {
		return nil
	}
	return p.sudo(ctx, "apt-get", "install", "-y", "uidmap")
}

func (p *podmanInstall) ensureArchive(ctx context.Context) error {
	_, err := os.Stat(p.archive)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return p.download(ctx)
	default:
		return fmt.Errorf("stat %s: %w", p.archive, err)
	}
}

func (p *podmanInstall) download(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, podmanDownloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return fmt.Errorf("create request for %s: %w", p.url, err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", p.url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected status %s", p.url, resp.Status)
	}
	return saveDownload(p.archive, resp.Body)
}

// saveDownload writes body to path through a partial file so a failed
// download never leaves a truncated archive behind.
func saveDownload(path string, body io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(path), setupDirPerm); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	partial := path + partialSuffix
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if err := writeFile(partial, flags, setupDownloadPerm, body); err != nil {
		_ = os.Remove(partial)
		return err
	}
	return os.Rename(partial, path)
}

// writeFile copies body into the file at path, opened with flags.
func writeFile(path string, flags int, perm fs.FileMode, body io.Reader) (err error) {
	f, err := os.OpenFile(path, flags, perm)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	if _, err := io.Copy(f, body); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func verifySHA256(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash %s: %w", path, err)
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return &checksumMismatchError{expected: expected, actual: actual}
	}
	return nil
}

// extractTarGz unpacks a gzipped tar archive below dest.
func extractTarGz(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return fmt.Errorf("open %s: %w", archive, err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read gzip: %w", err)
	}
	defer func() { _ = zr.Close() }()
	if err := os.MkdirAll(dest, setupDirPerm); err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	return extractTar(dest, tar.NewReader(zr))
}

func extractTar(dest string, tr *tar.Reader) error {
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		if err := extractEntry(dest, hdr, tr); err != nil {
			return err
		}
	}
}

func extractEntry(dest string, hdr *tar.Header, body io.Reader) error {
	name := filepath.FromSlash(hdr.Name)
	if !filepath.IsLocal(name) {
		return fmt.Errorf("%w: %q", errEscapesDest, hdr.Name)
	}
	target := filepath.Join(dest, name)
	if err := prepareParent(dest, target); err != nil {
		return err
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		return makeDir(dest, target, hdr.FileInfo().Mode().Perm())
	case tar.TypeReg:
		return extractRegular(target, hdr.FileInfo().Mode().Perm(), body)
	case tar.TypeSymlink:
		return extractSymlink(name, target, hdr.Linkname)
	default:
		return fmt.Errorf("%w: %q", errUnsupportedEntry, hdr.Name)
	}
}

// prepareParent creates the parent of target and checks that it resolves inside dest.
func prepareParent(dest, target string) error {
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, setupDirPerm); err != nil {
		return fmt.Errorf("create %s: %w", parent, err)
	}
	return ensureInside(dest, parent)
}

func makeDir(dest, target string, perm fs.FileMode) error {
	if err := os.MkdirAll(target, perm|setupOwnerDirBits); err != nil {
		return fmt.Errorf("create %s: %w", target, err)
	}
	return ensureInside(dest, target)
}

func extractRegular(target string, perm fs.FileMode, body io.Reader) error {
	if err := removeExisting(target); err != nil {
		return err
	}
	return writeFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm, body)
}

// extractSymlink accepts only relative links that stay inside the archive.
func extractSymlink(name, target, linkname string) error {
	if filepath.IsAbs(linkname) || !filepath.IsLocal(filepath.Join(filepath.Dir(name), linkname)) {
		return fmt.Errorf("%w: symlink %q points to %q", errEscapesDest, name, linkname)
	}
	if err := removeExisting(target); err != nil {
		return err
	}
	return os.Symlink(linkname, target)
}

// removeExisting deletes whatever sits at path so that the next create never
// writes through a symlink.
func removeExisting(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// ensureInside checks that path, with symlinks resolved, lies inside dest.
func ensureInside(dest, path string) error {
	realDest, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", dest, err)
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", path, err)
	}
	rel, err := filepath.Rel(realDest, realPath)
	if err != nil || !filepath.IsLocal(rel) {
		return fmt.Errorf("%w: %s resolves outside %s", errEscapesDest, path, dest)
	}
	return nil
}

// setupCLI implements `ci setup`.
func setupCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: ci setup <host|podman-linux> [flags]")
		return 2
	}
	switch args[0] {
	case "host":
		return setupHostCLI(args[1:], stdout, stderr)
	case "podman-linux":
		return setupPodmanCLI(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown setup subcommand: %s\n", args[0])
		return 2
	}
}

func setupHostCLI(args []string, stdout, stderr io.Writer) int {
	flags := setupFlagSet("setup host", stderr)
	label := flags.String("label", "", "suite label from suites.yaml")
	runner := flags.String(
		"runner",
		"",
		"runner label; required when the label runs on several runners",
	)
	if err := flags.Parse(args); err != nil {
		return setupFlagExit(err)
	}
	if !setupRequireFlags(stderr, flags, "label") {
		return 2
	}
	suite, err := setupFindSuite(*label, *runner)
	if err != nil {
		setupReport(stderr, err)
		return 1
	}
	if err := newHostSetup(
		stdout,
		stderr,
	).apply(context.Background(), suite.HostSetup); err != nil {
		setupReport(stderr, err)
		return 1
	}
	return 0
}

func setupPodmanCLI(args []string, stdout, stderr io.Writer) int {
	flags := setupFlagSet("setup podman-linux", stderr)
	archive := flags.String(
		"archive",
		"",
		"path of the podman-static archive; downloaded from --url when absent",
	)
	url := flags.String("url", "", "URL to download the archive from")
	sum := flags.String("sha256", "", "expected SHA-256 of the archive, hex encoded")
	extractDir := flags.String("extract-dir", "", "directory to extract the archive into")
	if err := flags.Parse(args); err != nil {
		return setupFlagExit(err)
	}
	if !setupRequireFlags(stderr, flags, "archive", "url", "sha256", "extract-dir") {
		return 2
	}
	p := &podmanInstall{
		archive:    *archive,
		url:        *url,
		sha256Hex:  *sum,
		extractDir: *extractDir,
		client:     http.DefaultClient,
		runner:     execRunner{},
		lookPath:   exec.LookPath,
		stdout:     stdout,
		stderr:     stderr,
	}
	if err := p.install(context.Background()); err != nil {
		setupReport(stderr, err)
		return 1
	}
	return 0
}

func setupFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

// setupFlagExit maps a flag parse error to an exit code; asking for help succeeds.
func setupFlagExit(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}

// setupRequireFlags reports the named flags that are empty and returns false when any is.
func setupRequireFlags(stderr io.Writer, flags *flag.FlagSet, names ...string) bool {
	var missing []string
	for _, name := range names {
		if f := flags.Lookup(name); f == nil || f.Value.String() == "" {
			missing = append(missing, "-"+name)
		}
	}
	if len(missing) == 0 {
		return true
	}
	_, _ = fmt.Fprintf(stderr, "missing required flags: %s\n", strings.Join(missing, ", "))
	return false
}

func setupFindSuite(label, runner string) (Suite, error) {
	m, err := LoadManifest()
	if err != nil {
		return Suite{}, err
	}
	return m.FindSuite(label, runner)
}

// setupReport prints err to stderr. A checksum mismatch is also emitted as a
// GitHub Actions error annotation.
func setupReport(stderr io.Writer, err error) {
	if _, ok := errors.AsType[*checksumMismatchError](err); ok {
		_, _ = fmt.Fprintf(stderr, "::error::%s\n", err)
		return
	}
	_, _ = fmt.Fprintf(stderr, "error: %s\n", err)
}
