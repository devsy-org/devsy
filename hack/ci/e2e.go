package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/e2e/framework/ginkgoreport"
)

const (
	defaultE2EDir        = "./e2e"
	e2eBinDirName        = "bin"
	e2eTestBinary        = "e2e.test"
	e2eTestBinaryWindows = "e2e.test.exe"
	devsyBinaryPrefix    = "devsy-"
	executableMode       = fs.FileMode(0o755)
	windowsDockerHost    = "npipe:////./pipe/podman-machine-default"
	windowsDockerWSLHost = "npipe:////./pipe/devsy-ci-no-daemon"
	wslgTestDistro       = "Ubuntu-24.04"
	wslgTestUser         = "devsyci"
	reportNameSuffix     = "-report.json"

	postCheckDockerInstalled = "docker-installed"
	dockerBinary             = "docker"
)

var (
	errNoDevsyBinaries   = errors.New("no devsy-* binaries found")
	errMicrosandboxSuite = errors.New("microsandbox suites are run by devsy-ci-microsandbox")
	errUnknownPostCheck  = errors.New("unknown post check")

	// postCheckCommands maps a suite post check to the commands it runs, in order.
	postCheckCommands = map[string][][]string{
		postCheckDockerInstalled: {{dockerBinary, "--version"}, {dockerBinary, "ps"}},
	}
)

// focusedChecksFunc matches runFocusedChecks. Tests substitute a fake.
type focusedChecksFunc func(context.Context, commandRunner, Suite, e2eOptions) error

// e2eRunner executes one Ginkgo suite through the e2e binary.
type e2eRunner struct {
	runner   commandRunner
	getenv   func(string) string
	readFile func(string) ([]byte, error)
	focused  focusedChecksFunc
	opts     e2eOptions
}

// e2eCLI implements `ci e2e prepare` and `ci e2e run`.
func e2eCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "usage: ci e2e <prepare|run> [flags]")
		return 2
	}
	switch args[0] {
	case "prepare":
		return e2ePrepareCLI(args[1:], stdout, stderr)
	case "run":
		return e2eRunCLI(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown e2e subcommand: %s\n", args[0])
		return 2
	}
}

func e2ePrepareCLI(args []string, stdout, stderr io.Writer) int {
	fl := newE2EFlagSet("ci e2e prepare", stderr)
	binSrc := fl.String("bin-src", "", "directory the devsy binaries were downloaded to")
	e2eDir := fl.String("e2e-dir", defaultE2EDir, "directory containing e2e.test")
	if code, ok := parseE2EFlags(fl, args); !ok {
		return code
	}
	if *binSrc == "" {
		_, _ = fmt.Fprintln(stderr, "ci e2e prepare: --bin-src is required")
		return 2
	}
	if err := prepareE2E(stdout, runtime.GOOS, *binSrc, *e2eDir); err != nil {
		_, _ = fmt.Fprintf(stderr, "ci e2e prepare: %v\n", err)
		return 1
	}
	return 0
}

func e2eRunCLI(args []string, stdout, stderr io.Writer) int {
	fl := newE2EFlagSet("ci e2e run", stderr)
	label := fl.String("label", "", "Ginkgo label of the suite to run")
	runnerName := fl.String("runner", "", "runner of the suite; needed when the label is ambiguous")
	e2eDir := fl.String("e2e-dir", defaultE2EDir, "directory containing e2e.test and bin/")
	if code, ok := parseE2EFlags(fl, args); !ok {
		return code
	}
	if *label == "" {
		_, _ = fmt.Fprintln(stderr, "ci e2e run: --label is required")
		return 2
	}
	suite, code := e2eLoadSuite(*label, *runnerName, stderr)
	if code != 0 {
		return code
	}
	r := e2eRunner{
		runner:   execRunner{},
		getenv:   os.Getenv,
		readFile: os.ReadFile,
		focused:  runFocusedChecks,
		opts: e2eOptions{
			E2EDir:  *e2eDir,
			TempDir: os.Getenv("RUNNER_TEMP"),
			GOOS:    runtime.GOOS,
			Stdout:  stdout,
			Stderr:  stderr,
		},
	}
	if err := r.run(context.Background(), suite); err != nil {
		_, _ = fmt.Fprintf(stderr, "ci e2e run %s: %v\n", *label, err)
		return 1
	}
	return 0
}

// e2eLoadSuite resolves the suite from the manifest and refuses the groups owned by other commands.
func e2eLoadSuite(label, runnerName string, stderr io.Writer) (Suite, int) {
	manifest, err := LoadManifest()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ci e2e run: %v\n", err)
		return Suite{}, 1
	}
	suite, err := manifest.FindSuite(label, runnerName)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "ci e2e run: %v\n", err)
		return Suite{}, 2
	}
	if suite.Group == groupMicrosandbox {
		_, _ = fmt.Fprintf(stderr, "ci e2e run: %v (label %q)\n", errMicrosandboxSuite, label)
		return Suite{}, 2
	}
	return suite, 0
}

func newE2EFlagSet(name string, output io.Writer) *flag.FlagSet {
	fl := flag.NewFlagSet(name, flag.ContinueOnError)
	fl.SetOutput(output)
	return fl
}

// parseE2EFlags parses the flags of one subcommand. It returns false together with the exit code
// when the subcommand must stop.
func parseE2EFlags(fl *flag.FlagSet, args []string) (int, bool) {
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, false
		}
		return 2, false
	}
	if fl.NArg() > 0 {
		_, _ = fmt.Fprintf(fl.Output(), "unexpected arguments: %v\n", fl.Args())
		return 2, false
	}
	return 0, true
}

// prepareE2E copies the devsy-* regular files found under binSrc flat into e2eDir/bin and makes
// them executable. It also marks e2e.test executable when it exists and the platform has exec bits.
func prepareE2E(stdout io.Writer, goos, binSrc, e2eDir string) error {
	found, err := e2eFindBinaries(stdout, binSrc)
	if err != nil {
		return fmt.Errorf("list %s: %w", binSrc, err)
	}
	if len(found) == 0 {
		return fmt.Errorf("%w under %s", errNoDevsyBinaries, binSrc)
	}
	binDir := filepath.Join(e2eDir, e2eBinDirName)
	if err := os.MkdirAll(binDir, executableMode); err != nil {
		return fmt.Errorf("create %s: %w", binDir, err)
	}
	for _, src := range found {
		dst := filepath.Join(binDir, filepath.Base(src))
		if err := e2eCopyExecutable(src, dst); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(stdout, "copied %s\n", dst)
	}
	return e2eMarkTestBinary(goos, e2eDir)
}

// e2eFindBinaries prints every entry under root, like `ls -R`, and returns the devsy-* regular files.
func e2eFindBinaries(stdout io.Writer, root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relative path of %s: %w", path, err)
		}
		_, _ = fmt.Fprintln(stdout, rel)
		if d.Type().IsRegular() && strings.HasPrefix(d.Name(), devsyBinaryPrefix) {
			found = append(found, path)
		}
		return nil
	})
	return found, err
}

func e2eCopyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, executableMode)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	_, copyErr := io.Copy(out, in)
	if err := errors.Join(copyErr, out.Close()); err != nil {
		return fmt.Errorf("copy %s: %w", src, err)
	}
	if err := os.Chmod(dst, executableMode); err != nil {
		return fmt.Errorf("chmod %s: %w", dst, err)
	}
	return nil
}

// e2eMarkTestBinary adds the execute bits to e2e.test when it exists. Windows has no execute bits.
func e2eMarkTestBinary(goos, e2eDir string) error {
	if goos == osWindows {
		return nil
	}
	path := filepath.Join(e2eDir, e2eTestBinary)
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if err := os.Chmod(path, info.Mode().Perm()|0o111); err != nil {
		return fmt.Errorf("chmod %s: %w", path, err)
	}
	return nil
}

// run executes one suite: focused checks, optional rootless diagnostics, the e2e binary, the
// report check and then the suite's post checks.
func (r e2eRunner) run(ctx context.Context, suite Suite) error {
	r.printBanner(suite)
	if err := r.focused(ctx, r.runner, suite, r.opts); err != nil {
		return fmt.Errorf("focused checks: %w", err)
	}
	if suite.InstallPodman == podmanRootless {
		if err := r.printRootlessDiagnostics(ctx); err != nil {
			return fmt.Errorf("rootless diagnostics: %w", err)
		}
	}
	if err := r.runner.Run(ctx, r.binarySpec(suite)); err != nil {
		return fmt.Errorf("e2e run: %w", err)
	}
	if err := r.checkReport(suite); err != nil {
		return err
	}
	for _, check := range suite.PostChecks {
		if err := r.postCheck(ctx, check); err != nil {
			return fmt.Errorf("post check %s: %w", check, err)
		}
	}
	return nil
}

// checkReport verifies the Ginkgo JSON report of a suite that pins its spec count. It runs after
// the binary succeeded and before the post checks.
func (r e2eRunner) checkReport(suite Suite) error {
	if !reportEnabled(suite) {
		return nil
	}
	name := reportFileName(suite.Label)
	data, err := r.readFile(filepath.Join(r.opts.E2EDir, name))
	if err != nil {
		return fmt.Errorf("read report %s: %w", name, err)
	}
	if err := ginkgoreport.Check(data, suite.Label, suite.ExpectedSpecCount); err != nil {
		return fmt.Errorf("verify report %s: %w", name, err)
	}
	_, _ = fmt.Fprintf(r.opts.Stdout, "report check passed: %d specs\n", suite.ExpectedSpecCount)
	return nil
}

// reportEnabled reports whether the suite's selected specs are verified from a Ginkgo JSON report.
func reportEnabled(suite Suite) bool {
	return suite.ExpectedSpecCount > 0 && suite.Group != groupMicrosandbox
}

// reportFileName is the JSON report name, relative to the e2e directory the binary runs in.
func reportFileName(label string) string {
	return label + reportNameSuffix
}

func (r e2eRunner) printBanner(suite Suite) {
	_, _ = fmt.Fprintf(
		r.opts.Stdout,
		"running suite %s on %s (privileged=%t)\n",
		suite.Label,
		suite.Runner,
		suite.Resolved,
	)
}

func (r e2eRunner) printRootlessDiagnostics(ctx context.Context) error {
	_, _ = fmt.Fprintln(r.opts.Stdout, "Podman rootless test identity:")
	if err := r.runner.Run(ctx, r.hostCommand("id")); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(r.opts.Stdout, "HOME=%s\n", r.getenv("HOME"))
	_, _ = fmt.Fprintf(r.opts.Stdout, "XDG_RUNTIME_DIR=%s\n", r.getenv("XDG_RUNTIME_DIR"))
	return r.runner.Run(ctx, r.hostCommand("podman", "info"))
}

// binarySpec returns the command that runs the e2e binary for the suite. On Linux a privileged
// suite runs through sudo, with the environment passed as K=V arguments.
func (r e2eRunner) binarySpec(suite Suite) commandSpec {
	name := "./" + e2eTestBinary
	if r.opts.GOOS == osWindows {
		name = "./" + e2eTestBinaryWindows
	}
	args := e2eTestArgs(suite)
	env := r.e2eEnv(suite)
	spec := commandSpec{
		Name:   name,
		Args:   args,
		Dir:    r.opts.E2EDir,
		Stdout: r.opts.Stdout,
		Stderr: r.opts.Stderr,
	}
	if r.opts.GOOS != osLinux || !suite.Resolved {
		spec.Env = env
		return spec
	}
	spec.Name = "sudo"
	spec.Args = slices.Concat(env, []string{name}, args)
	return spec
}

// e2eEnv returns the variables the e2e binary needs, as K=V pairs in a fixed order.
func (r e2eRunner) e2eEnv(suite Suite) []string {
	env := []string{
		"GH_USERNAME=" + r.getenv("GH_USERNAME"),
		"GH_ACCESS_TOKEN=" + r.getenv("GH_ACCESS_TOKEN"),
		"GH_CREDENTIAL_USERNAME=" + r.getenv("GH_CREDENTIAL_USERNAME"),
		"KUBECONFIG=" + r.kubeconfig(),
		"PATH=" + r.getenv("PATH"),
	}
	return append(env, r.dockerEnv(suite)...)
}

// dockerEnv returns the Docker client variables of the suite. A docker-wsl suite talks to the
// WSL2 fixture and clears DOCKER_CONTEXT so that the explicit host is used. It also names the
// fixture distro and user for the WSLg specs.
func (r e2eRunner) dockerEnv(suite Suite) []string {
	if suite.Runtime == runtimeDockerWSL {
		return []string{
			"DOCKER_HOST=" + windowsDockerWSLHost,
			"DOCKER_CONTEXT=",
			"DEVSY_WSLG_TEST_DISTRO=" + wslgTestDistro,
			"DEVSY_WSLG_TEST_USER=" + wslgTestUser,
		}
	}
	if dockerHost := r.dockerHost(); dockerHost != "" {
		return []string{"DOCKER_HOST=" + dockerHost}
	}
	return nil
}

func (r e2eRunner) kubeconfig() string {
	if kubeconfig := r.getenv("KUBECONFIG"); kubeconfig != "" {
		return kubeconfig
	}
	home := r.getenv("HOME")
	if home == "" && r.opts.GOOS == osWindows {
		home = r.getenv("USERPROFILE")
	}
	return filepath.Join(home, ".kube", "config")
}

func (r e2eRunner) dockerHost() string {
	if r.opts.GOOS == osWindows {
		return windowsDockerHost
	}
	return r.getenv("DOCKER_HOST")
}

// e2eTestArgs returns the Go test and Ginkgo flags for one suite. A suite with a pinned spec count
// also writes its JSON report next to the binary.
func e2eTestArgs(suite Suite) []string {
	args := []string{
		"-test.v",
		"-ginkgo.v",
		"-test.timeout", suite.TestTimeout,
		"-ginkgo.fail-on-empty",
		fmt.Sprintf("-ginkgo.flake-attempts=%d", suite.FlakeAttempts),
		"--ginkgo.label-filter=" + suite.Label,
	}
	if reportEnabled(suite) {
		args = append(args, "-ginkgo.json-report="+reportFileName(suite.Label))
	}
	return args
}

func (r e2eRunner) postCheck(ctx context.Context, name string) error {
	commands, ok := postCheckCommands[name]
	if !ok {
		return fmt.Errorf("%w %q", errUnknownPostCheck, name)
	}
	for _, argv := range commands {
		if err := r.runner.Run(ctx, r.hostCommand(argv[0], argv[1:]...)); err != nil {
			return err
		}
	}
	return nil
}

// hostCommand returns a spec that runs in the current directory and writes to the run's output.
func (r e2eRunner) hostCommand(name string, args ...string) commandSpec {
	return commandSpec{Name: name, Args: args, Stdout: r.opts.Stdout, Stderr: r.opts.Stderr}
}
