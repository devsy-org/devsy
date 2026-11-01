package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const (
	goListFormat          = "{{.ImportPath}}|{{.Dir}}"
	gitHubActionsEnv      = "GITHUB_ACTIONS"
	hostShellBusybox      = "busybox"
	shellPath             = "/bin/sh"
	busyboxPath           = "/bin/busybox"
	cmdGo                 = "go"
	cmdSudo               = "sudo"
	cmdReadlink           = "readlink"
	cmdLn                 = "ln"
	unitFlagSetName       = "unit"
	unitUsageExitCode     = 2
	unitFailureExitCode   = 1
	unitSuccessExitCode   = 0
	unitTestPatternFormat = "^(%s)$"
)

var (
	errNoUnitSteps         = errors.New("no unit steps selected")
	errNoUnitTestPackages  = errors.New("no unit-test packages found")
	errOutsideRepository   = errors.New("package directory is outside the repository")
	errMalformedGoList     = errors.New("unexpected go list line")
	errRequiredTestMissing = errors.New("required test not listed by go test -list")
	errUnknownHostShell    = errors.New("unknown host_shell")
	errHostShellNeedsLinux = errors.New("host_shell needs Linux")
	errShellNotSymlink     = errors.New("shell is not a symlink")
	errUnexpectedUnitArgs  = errors.New("unexpected positional arguments")
	excludedPackagePattern = regexp.MustCompile(`(^|/)(test|e2e)(/|$)`)
)

// unitFlags holds the parsed flags of `ci unit`.
type unitFlags struct {
	goos         string
	step         string
	coverprofile string
	list         bool
}

// unitRunner runs the selected unit steps through an injected command runner.
type unitRunner struct {
	runner       commandRunner
	goos         string
	root         string
	env          func(string) string
	coverprofile string
	stdout       io.Writer
	stderr       io.Writer
}

func unitCLI(args []string, stdout, stderr io.Writer) int {
	f, err := parseUnitFlags(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return unitSuccessExitCode
	}
	if err != nil {
		return unitUsageExitCode
	}
	m, err := LoadManifest()
	if err != nil {
		return reportUnitError(stderr, err)
	}
	steps, err := selectUnitSteps(m.Unit.Steps, f.goos, f.step)
	if err != nil {
		return reportUnitError(stderr, err)
	}
	root, err := os.Getwd()
	if err != nil {
		return reportUnitError(stderr, err)
	}
	u := unitRunner{
		runner:       execRunner{},
		goos:         f.goos,
		root:         root,
		env:          os.Getenv,
		coverprofile: f.coverprofile,
		stdout:       stdout,
		stderr:       stderr,
	}
	return u.runSelected(steps, f.list)
}

func reportUnitError(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintln(stderr, err)
	return unitFailureExitCode
}

func parseUnitFlags(args []string, stderr io.Writer) (unitFlags, error) {
	var f unitFlags
	fs := flag.NewFlagSet(unitFlagSetName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.goos, "os", runtime.GOOS, "GOOS whose steps are selected")
	fs.StringVar(&f.step, "step", "", "run only the step with this name")
	fs.StringVar(&f.coverprofile, "coverprofile", "", "coverage profile for discovered packages")
	fs.BoolVar(&f.list, "list", false, "print the selected step names and exit")
	if err := fs.Parse(args); err != nil {
		return unitFlags{}, err
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "unit: unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return unitFlags{}, errUnexpectedUnitArgs
	}
	return f, nil
}

// selectUnitSteps returns the steps that run on goos, narrowed to name when
// name is not empty. Manifest order is kept.
func selectUnitSteps(steps []UnitStep, goos, name string) ([]UnitStep, error) {
	var selected []UnitStep
	for _, step := range steps {
		if step.RunsOn(goos) && (name == "" || step.Name == name) {
			selected = append(selected, step)
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("%w: os %s, step %q", errNoUnitSteps, goos, name)
	}
	return selected, nil
}

func (u unitRunner) runSelected(steps []UnitStep, list bool) int {
	if list {
		for _, step := range steps {
			_, _ = fmt.Fprintln(u.stdout, step.Name)
		}
		return unitSuccessExitCode
	}
	return u.runAll(steps)
}

// runAll runs every step, even after a failure, and reports each failed step.
func (u unitRunner) runAll(steps []UnitStep) int {
	var failures []error
	for _, step := range steps {
		if err := u.runStep(step); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", step.Name, err))
		}
	}
	for _, err := range failures {
		_, _ = fmt.Fprintf(u.stderr, "FAILED: %v\n", err)
	}
	if len(failures) > 0 {
		return unitFailureExitCode
	}
	return unitSuccessExitCode
}

func (u unitRunner) runStep(step UnitStep) error {
	u.begin(step)
	defer u.end()
	if err := u.checkHostShell(step); err != nil {
		return err
	}
	packages, err := u.packagesFor(step)
	if err != nil {
		return err
	}
	if err = u.checkRequiredTests(step, packages); err != nil {
		return err
	}
	return u.withHostShell(step, func() error { return u.goTest(step, packages) })
}

func (u unitRunner) inActions() bool {
	return u.env(gitHubActionsEnv) == "true"
}

func (u unitRunner) begin(step UnitStep) {
	if u.inActions() {
		_, _ = fmt.Fprintf(u.stdout, "::group::%s\n", step.Name)
		return
	}
	_, _ = fmt.Fprintf(u.stdout, "=== %s: %s\n", step.Name, step.Description)
}

func (u unitRunner) end() {
	if u.inActions() {
		_, _ = fmt.Fprintln(u.stdout, "::endgroup::")
	}
}

// checkHostShell rejects host_shell values this runner cannot provide, before
// any command runs.
func (u unitRunner) checkHostShell(step UnitStep) error {
	switch step.HostShell {
	case "":
		return nil
	case hostShellBusybox:
		if u.goos != osLinux {
			return fmt.Errorf("%w: %s on %s", errHostShellNeedsLinux, hostShellBusybox, u.goos)
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", errUnknownHostShell, step.HostShell)
	}
}

func (u unitRunner) withHostShell(step UnitStep, run func() error) error {
	if step.HostShell != hostShellBusybox {
		return run()
	}
	if !u.inActions() {
		_, _ = fmt.Fprintf(
			u.stdout,
			"skipping %s: host_shell %s runs only in GitHub Actions\n",
			step.Name,
			hostShellBusybox,
		)
		return nil
	}
	return u.withBusyboxShell(run)
}

// withBusyboxShell points /bin/sh at BusyBox for the duration of run. The
// original target is restored even when run fails.
func (u unitRunner) withBusyboxShell(run func() error) (err error) {
	if err = u.run(cmdSudo, "apt-get", "update", "-qq"); err != nil {
		return err
	}
	if err = u.run(cmdSudo, "apt-get", "install", "-y", "busybox-static"); err != nil {
		return err
	}
	original, err := u.shellTarget()
	if err != nil {
		return err
	}
	defer func() {
		if restoreErr := u.run(cmdSudo, cmdLn, "-sf", original, shellPath); restoreErr != nil {
			err = errors.Join(err, restoreErr)
		}
	}()
	if err = u.run(cmdSudo, cmdLn, "-sf", busyboxPath, shellPath); err != nil {
		return err
	}
	return run()
}

func (u unitRunner) shellTarget() (string, error) {
	out, err := u.capture(cmdReadlink, shellPath)
	if err != nil {
		return "", fmt.Errorf("readlink %s: %w", shellPath, err)
	}
	target := strings.TrimSpace(out)
	if target == "" {
		return "", fmt.Errorf("%w: %s", errShellNotSymlink, shellPath)
	}
	return target, nil
}

func (u unitRunner) packagesFor(step UnitStep) ([]string, error) {
	if !step.Discover {
		return step.Packages, nil
	}
	goList, err := u.capture(cmdGo, "list", "-f", goListFormat, "./...")
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	return filterUnitTestPackages(goList, u.root)
}

// checkRequiredTests fails the step when a required test name is not an exact
// line of `go test -list`, so a renamed test cannot silently match nothing.
func (u unitRunner) checkRequiredTests(step UnitStep, packages []string) error {
	if len(step.RequireTests) == 0 {
		return nil
	}
	args := append([]string{"test"}, packages...)
	args = append(args, "-list", unitTestPattern(step))
	listing, err := u.capture(cmdGo, args...)
	if err != nil {
		return fmt.Errorf("go test -list: %w", err)
	}
	if missing := missingTests(step.RequireTests, listing); len(missing) > 0 {
		return fmt.Errorf("%w: %s", errRequiredTestMissing, strings.Join(missing, ", "))
	}
	return nil
}

func unitTestPattern(step UnitStep) string {
	if step.Run != "" {
		return step.Run
	}
	return fmt.Sprintf(unitTestPatternFormat, strings.Join(step.RequireTests, "|"))
}

// missingTests returns the required names that are not an exact line of listing.
func missingTests(required []string, listing string) []string {
	present := map[string]bool{}
	for line := range strings.SplitSeq(listing, "\n") {
		present[strings.TrimSuffix(line, "\r")] = true
	}
	var missing []string
	for _, name := range required {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

func (u unitRunner) goTest(step UnitStep, packages []string) error {
	return u.run(cmdGo, u.goTestArgs(step, packages)...)
}

func (u unitRunner) goTestArgs(step UnitStep, packages []string) []string {
	args := append([]string{"test"}, step.Args...)
	if step.Run != "" {
		args = append(args, "-run", step.Run)
	}
	if u.coverprofile != "" && step.Discover {
		args = append(args, "-coverprofile="+u.coverprofile, "-covermode=atomic")
	}
	return append(args, packages...)
}

// filterUnitTestPackages takes `go list -f '{{.ImportPath}}|{{.Dir}}'` output
// and returns the import paths of packages under root, skipping any package
// whose directory has a test or e2e path segment below root.
func filterUnitTestPackages(goList, root string) ([]string, error) {
	var packages []string
	for line := range strings.SplitSeq(goList, "\n") {
		if line == "" {
			continue
		}
		importPath, dir, ok := strings.Cut(line, "|")
		if !ok {
			return nil, fmt.Errorf("%w: %q", errMalformedGoList, line)
		}
		if !isUnderRoot(dir, root) {
			return nil, fmt.Errorf("%w: %s", errOutsideRepository, dir)
		}
		relative := filepath.ToSlash(strings.TrimPrefix(dir, root))
		if excludedPackagePattern.MatchString(relative) {
			continue
		}
		packages = append(packages, importPath)
	}
	if len(packages) == 0 {
		return nil, errNoUnitTestPackages
	}
	return packages, nil
}

// isUnderRoot reports whether dir is root or a directory inside it.
// A sibling sharing root as a string prefix (root+"2") is not under root.
func isUnderRoot(dir, root string) bool {
	return dir == root || strings.HasPrefix(dir, root+string(filepath.Separator))
}

func (u unitRunner) run(name string, args ...string) error {
	return u.runner.Run(context.Background(), commandSpec{
		Name:   name,
		Args:   args,
		Dir:    u.root,
		Stdout: u.stdout,
		Stderr: u.stderr,
	})
}

// capture runs a command and returns its stdout. Stderr is passed through.
func (u unitRunner) capture(name string, args ...string) (string, error) {
	var out bytes.Buffer
	err := u.runner.Run(context.Background(), commandSpec{
		Name:   name,
		Args:   args,
		Dir:    u.root,
		Stdout: &out,
		Stderr: u.stderr,
	})
	return out.String(), err
}
