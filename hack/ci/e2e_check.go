package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// e2eOptions is the environment shared by `ci e2e run` and the focused checks.
type e2eOptions struct {
	E2EDir  string // directory containing e2e.test and bin/
	TempDir string // RUNNER_TEMP; reports are written here
	GOOS    string
	Stdout  io.Writer
	Stderr  io.Writer
}

var (
	errFocusedUID     = errors.New("focused check uid mismatch")
	errFocusedGID     = errors.New("focused check gid mismatch")
	errFocusedAccount = errors.New("current user or group does not resolve")
	errFocusedDir     = errors.New("directory is missing or not writable")
	errFocusedOwner   = errors.New("directory is not owned by the current user")

	errReportRead     = errors.New("read ginkgo json report")
	errReportShape    = errors.New("unexpected suite report shape")
	errReportDryRun   = errors.New("dry-run is not execution")
	errReportSuite    = errors.New("suite failed")
	errReportLeaves   = errors.New("expected one executed leaf")
	errReportLeafText = errors.New("wrong focused spec")
	errReportState    = errors.New("focused spec did not pass")
	errReportRetried  = errors.New("unexpected retries")
)

// focusedChecksumFiles are the binaries whose digests are printed before each focused run.
var focusedChecksumFiles = []string{"e2e.test", "bin/devsy-linux-amd64"}

// focusedHost is the process identity and account database the preflight inspects.
type focusedHost struct {
	uid         int
	gid         int
	home        string
	lookupUser  func(id string) (*user.User, error)
	lookupGroup func(id string) (*user.Group, error)
}

// focusedHostProvider returns the host focused checks run under. Tests replace it.
var focusedHostProvider = currentFocusedHost

func currentFocusedHost() focusedHost {
	return focusedHost{
		uid:         os.Getuid(),
		gid:         os.Getgid(),
		home:        os.Getenv("HOME"),
		lookupUser:  user.LookupId,
		lookupGroup: user.LookupGroupId,
	}
}

// focusedRun carries what every focused check of one suite needs.
type focusedRun struct {
	runner commandRunner
	suite  Suite
	opts   e2eOptions
}

// runFocusedChecks runs every focused check of the suite, in order.
func runFocusedChecks(
	ctx context.Context,
	runner commandRunner,
	suite Suite,
	opts e2eOptions,
) error {
	run := focusedRun{runner: runner, suite: suite, opts: opts}
	for _, check := range suite.FocusedChecks {
		if err := run.runOne(ctx, check); err != nil {
			return fmt.Errorf("focused check %s: %w", check.Name, err)
		}
		_, _ = fmt.Fprintf(opts.Stdout, "focused check %s passed in one attempt\n", check.Name)
	}
	return nil
}

func (r focusedRun) runOne(ctx context.Context, check FocusedCheck) error {
	if r.opts.GOOS == osLinux {
		if err := focusedHostProvider().preflight(check, r.opts); err != nil {
			return fmt.Errorf("preflight: %w", err)
		}
	}
	if err := r.diagnostics(ctx); err != nil {
		return err
	}
	reportPath, err := filepath.Abs(filepath.Join(r.opts.TempDir, check.Name+".json"))
	if err != nil {
		return fmt.Errorf("resolve report path: %w", err)
	}
	if err := r.runner.Run(ctx, r.e2eCommand(check, reportPath)); err != nil {
		return fmt.Errorf("run e2e binary: %w", err)
	}
	return verifyFocusedReport(reportPath, check)
}

// diagnostics logs the Docker, checkout and binary identity the CI log needs.
func (r focusedRun) diagnostics(ctx context.Context) error {
	if err := r.exec(ctx, "docker", "info"); err != nil {
		return err
	}
	if err := r.exec(ctx, "docker", "compose", "version"); err != nil {
		return err
	}
	sha, err := r.checkout(ctx)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(r.opts.Stdout, "checkout=%s\n", sha)
	return printChecksums(r.opts)
}

// exec runs a command in the e2e directory, streaming its output.
func (r focusedRun) exec(ctx context.Context, name string, args ...string) error {
	spec := commandSpec{
		Name:   name,
		Args:   args,
		Dir:    r.opts.E2EDir,
		Stdout: r.opts.Stdout,
		Stderr: r.opts.Stderr,
	}
	if err := r.runner.Run(ctx, spec); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(append([]string{name}, args...), " "), err)
	}
	return nil
}

// checkout returns the commit the e2e directory is checked out at.
func (r focusedRun) checkout(ctx context.Context) (string, error) {
	var out bytes.Buffer
	spec := commandSpec{
		Name:   "git",
		Args:   []string{"rev-parse", "HEAD"},
		Dir:    r.opts.E2EDir,
		Stdout: &out,
		Stderr: r.opts.Stderr,
	}
	if err := r.runner.Run(ctx, spec); err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(out.String()), nil
}

// e2eCommand is the direct, unprivileged run of the e2e binary for one check.
func (r focusedRun) e2eCommand(check FocusedCheck, reportPath string) commandSpec {
	return commandSpec{
		Name: "./e2e.test",
		Args: []string{
			"-test.v",
			"-ginkgo.v",
			"-test.timeout", r.suite.TestTimeout,
			"-ginkgo.fail-on-empty",
			"-ginkgo.flake-attempts=1",
			"--ginkgo.label-filter=" + r.suite.Label,
			"--ginkgo.focus=" + check.Focus,
			"--ginkgo.json-report=" + reportPath,
		},
		Dir:    r.opts.E2EDir,
		Stdout: r.opts.Stdout,
		Stderr: r.opts.Stderr,
	}
}

// preflight verifies the identity and directories a focused check depends on.
func (h focusedHost) preflight(check FocusedCheck, opts e2eOptions) error {
	_, _ = fmt.Fprintf(opts.Stdout, "uid=%d gid=%d\n", h.uid, h.gid)
	if err := h.checkRequiredIDs(check); err != nil {
		return err
	}
	if err := h.checkAccount(); err != nil {
		return err
	}
	if err := h.checkHome(); err != nil {
		return err
	}
	if opts.TempDir == "" {
		return nil
	}
	return requireWritableDir(opts.TempDir)
}

func (h focusedHost) checkRequiredIDs(check FocusedCheck) error {
	if check.RequireUID != 0 && h.uid != check.RequireUID {
		return fmt.Errorf("%w: uid %d, want %d", errFocusedUID, h.uid, check.RequireUID)
	}
	if check.RequireGID != 0 && h.gid != check.RequireGID {
		return fmt.Errorf("%w: gid %d, want %d", errFocusedGID, h.gid, check.RequireGID)
	}
	return nil
}

func (h focusedHost) checkAccount() error {
	if _, err := h.lookupUser(strconv.Itoa(h.uid)); err != nil {
		return fmt.Errorf("%w: uid %d: %w", errFocusedAccount, h.uid, err)
	}
	if _, err := h.lookupGroup(strconv.Itoa(h.gid)); err != nil {
		return fmt.Errorf("%w: gid %d: %w", errFocusedAccount, h.gid, err)
	}
	return nil
}

func (h focusedHost) checkHome() error {
	if err := requireWritableDir(h.home); err != nil {
		return err
	}
	return requireOwnedBy(h.home, h.uid)
}

// requireWritableDir fails unless path is an existing directory the process can write to.
func requireWritableDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", errFocusedDir, path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", errFocusedDir, path)
	}
	return probeWritable(path)
}

// probeWritable creates and removes a temporary file in dir.
func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".focused-check-*")
	if err != nil {
		return fmt.Errorf("%w: %s: %w", errFocusedDir, dir, err)
	}
	closeErr := f.Close()
	removeErr := os.Remove(f.Name())
	if joined := errors.Join(closeErr, removeErr); joined != nil {
		return fmt.Errorf("%w: %s: %w", errFocusedDir, dir, joined)
	}
	return nil
}

// printChecksums prints `<sha256>  ./<file>` for each binary that exists.
func printChecksums(opts e2eOptions) error {
	for _, rel := range focusedChecksumFiles {
		sum, found, err := fileSHA256(filepath.Join(opts.E2EDir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		if found {
			_, _ = fmt.Fprintf(opts.Stdout, "%s  ./%s\n", sum, rel)
		}
	}
	return nil
}

// fileSHA256 hashes a file; found is false when the file does not exist.
func fileSHA256(path string) (string, bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false, fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), true, nil
}

// ginkgoJSONSuite is the subset of a Ginkgo JSON suite report the check reads.
type ginkgoJSONSuite struct {
	SuiteConfig    *ginkgoJSONConfig `json:"SuiteConfig"`
	SuiteSucceeded *bool             `json:"SuiteSucceeded"`
	SpecReports    []ginkgoJSONSpec  `json:"SpecReports"`
}

type ginkgoJSONConfig struct {
	DryRun *bool `json:"DryRun"`
}

type ginkgoJSONSpec struct {
	LeafNodeType string `json:"LeafNodeType"`
	LeafNodeText string `json:"LeafNodeText"`
	State        string `json:"State"`
	NumAttempts  int    `json:"NumAttempts"`
}

// verifyFocusedReport validates a Ginkgo JSON report against a check.
func verifyFocusedReport(path string, check FocusedCheck) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: %w", errReportRead, err)
	}
	var suites []ginkgoJSONSuite
	if err := json.Unmarshal(data, &suites); err != nil {
		return fmt.Errorf("%w: %w", errReportShape, err)
	}
	if len(suites) != 1 {
		return fmt.Errorf("%w: found %d suite reports", errReportShape, len(suites))
	}
	return checkSuiteReport(suites[0], check)
}

func checkSuiteReport(s ginkgoJSONSuite, check FocusedCheck) error {
	if s.SuiteConfig == nil || s.SuiteConfig.DryRun == nil || s.SuiteSucceeded == nil {
		return fmt.Errorf("%w: missing SuiteConfig.DryRun or SuiteSucceeded", errReportShape)
	}
	if *s.SuiteConfig.DryRun {
		return errReportDryRun
	}
	if !*s.SuiteSucceeded {
		return errReportSuite
	}
	return checkFocusedSpec(executedLeaves(s.SpecReports), check)
}

// executedLeaves returns the It nodes that were not skipped.
func executedLeaves(specs []ginkgoJSONSpec) []ginkgoJSONSpec {
	var leaves []ginkgoJSONSpec
	for _, spec := range specs {
		if spec.LeafNodeType == "It" && spec.State != "skipped" {
			leaves = append(leaves, spec)
		}
	}
	return leaves
}

func checkFocusedSpec(leaves []ginkgoJSONSpec, check FocusedCheck) error {
	if len(leaves) != 1 {
		return fmt.Errorf("%w: found %d", errReportLeaves, len(leaves))
	}
	spec := leaves[0]
	if spec.LeafNodeText != check.ExpectLeaf {
		return fmt.Errorf("%w: %q, want %q", errReportLeafText, spec.LeafNodeText, check.ExpectLeaf)
	}
	if spec.State != "passed" {
		return fmt.Errorf("%w: state %q", errReportState, spec.State)
	}
	if spec.NumAttempts != 1 {
		return fmt.Errorf("%w: %d attempts", errReportRetried, spec.NumAttempts)
	}
	return nil
}
