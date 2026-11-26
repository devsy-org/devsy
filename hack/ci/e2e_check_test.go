package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

const (
	focusedTestName    = "compose-uid-nonroot"
	focusedTestLabel   = "up-docker-compose"
	focusedTestLeaf    = "implements updateRemoteUserUID with root container user"
	focusedTestFocus   = focusedTestLeaf + "$"
	focusedTestTimeout = "1500s"
	focusedTestBinary  = "test binary"
	focusedTestAgent   = "agent"

	focusedE2EBinary  = "./e2e.test"
	focusedGitBinary  = "git"
	focusedCompose    = "compose"
	focusedVersion    = "version"
	focusedInfo       = "info"
	focusedRevParse   = "rev-parse"
	focusedHEAD       = "HEAD"
	focusedTestV      = "-test.v"
	focusedGinkgoV    = "-ginkgo.v"
	focusedTestTO     = "-test.timeout"
	focusedFailOnEmpt = "-ginkgo.fail-on-empty"
	focusedFlakeOne   = "-ginkgo.flake-attempts=1"
	focusedLabelArg   = "--ginkgo.label-filter="
	focusedFocusArg   = "--ginkgo.focus="
	focusedReportArgP = "--ginkgo.json-report="
	focusedMalformed  = "malformed JSON"

	focusedLeafPassed  = `{"LeafNodeType":"It","LeafNodeText":"` + focusedTestLeaf + `","State":"passed","NumAttempts":1}`
	focusedLeafSkipped = `{"LeafNodeType":"It","LeafNodeText":"` + focusedTestLeaf + `","State":"skipped","NumAttempts":0}`
	focusedBeforeSuite = `{"LeafNodeType":"BeforeSuite","LeafNodeText":"","State":"failed","NumAttempts":0}`
)

// focusedCall is one command seen by focusedRecorder.
type focusedCall struct {
	name string
	args []string
	dir  string
}

// focusedRecorder is a commandRunner that records calls. The e2e binary writes
// report to the --ginkgo.json-report path it is given, then returns runErr.
type focusedRecorder struct {
	calls  []focusedCall
	report string
	runErr error
}

func newFocusedRecorder() *focusedRecorder {
	return &focusedRecorder{report: focusedReport(false, true, focusedLeafPassed)}
}

func (f *focusedRecorder) Run(_ context.Context, spec commandSpec) error {
	f.calls = append(f.calls, focusedCall{name: spec.Name, args: spec.Args, dir: spec.Dir})
	switch spec.Name {
	case focusedGitBinary:
		_, _ = io.WriteString(spec.Stdout, "abc123\n")
	case focusedE2EBinary:
		return f.runE2E(spec.Args)
	}
	return nil
}

func (f *focusedRecorder) runE2E(args []string) error {
	path, ok := focusedReportArg(args)
	if !ok {
		return errors.New("missing --ginkgo.json-report argument")
	}
	if err := os.WriteFile(path, []byte(f.report), 0o600); err != nil {
		return fmt.Errorf("write fake report: %w", err)
	}
	return f.runErr
}

func focusedReportArg(args []string) (string, bool) {
	for _, arg := range args {
		if path, ok := strings.CutPrefix(arg, focusedReportArgP); ok {
			return path, true
		}
	}
	return "", false
}

// focusedReport builds a one-suite Ginkgo JSON report from raw spec objects.
func focusedReport(dryRun, succeeded bool, specs ...string) string {
	return "[" + focusedSuiteObject(dryRun, succeeded, specs...) + "]"
}

// focusedSuiteObject builds one suite object of a Ginkgo JSON report.
func focusedSuiteObject(dryRun, succeeded bool, specs ...string) string {
	return fmt.Sprintf(`{"SuiteConfig":{"DryRun":%t},"SuiteSucceeded":%t,"SpecReports":[%s]}`,
		dryRun, succeeded, strings.Join(specs, ","))
}

func focusedTestCheck() FocusedCheck {
	return FocusedCheck{Name: focusedTestName, Focus: focusedTestFocus, ExpectLeaf: focusedTestLeaf}
}

func focusedTestSuite(check FocusedCheck) Suite {
	return Suite{
		Label:         focusedTestLabel,
		TestTimeout:   focusedTestTimeout,
		FocusedChecks: []FocusedCheck{check},
	}
}

func focusedSum(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func focusedLookupUserFails(_ string) (*user.User, error) {
	return nil, errors.New("no such user")
}

func focusedLookupGroupFails(_ string) (*user.Group, error) {
	return nil, errors.New("no such group")
}

type focusedCheckSuite struct {
	suite.Suite
	check FocusedCheck
}

type focusedPreflightCase struct {
	name    string
	mutate  func(h *focusedHost, c *FocusedCheck, o *e2eOptions)
	wantErr error
}

type focusedReportCase struct {
	name    string
	report  string
	wantErr error
}

func TestFocusedCheckSuite(t *testing.T) {
	suite.Run(t, new(focusedCheckSuite))
}

func (s *focusedCheckSuite) SetupSuite() {
	s.check = focusedTestCheck()
}

func (s *focusedCheckSuite) TestRunsDiagnosticsThenE2EWithoutSudo() {
	opts, stdout := s.options("linux")
	host := s.passingHost()
	s.installHost(host)
	recorder := newFocusedRecorder()

	s.Require().
		NoError(runFocusedChecks(context.Background(), recorder, focusedTestSuite(s.checkFor(host)), opts))

	reportPath := filepath.Join(opts.TempDir, focusedTestName+".json")
	s.Equal([]focusedCall{
		{name: dockerBinary, args: []string{focusedInfo}, dir: opts.E2EDir},
		{name: dockerBinary, args: []string{focusedCompose, focusedVersion}, dir: opts.E2EDir},
		{name: focusedGitBinary, args: []string{focusedRevParse, focusedHEAD}, dir: opts.E2EDir},
		{
			name: focusedE2EBinary,
			args: []string{
				focusedTestV,
				focusedGinkgoV,
				focusedTestTO, focusedTestTimeout,
				focusedFailOnEmpt,
				focusedFlakeOne,
				focusedLabelArg + focusedTestLabel,
				focusedFocusArg + focusedTestFocus,
				focusedReportArgP + reportPath,
			},
			dir: opts.E2EDir,
		},
	}, recorder.calls)
	for _, call := range recorder.calls {
		s.NotEqual("sudo", call.name)
	}

	out := stdout.String()
	s.Contains(out, fmt.Sprintf("uid=%d gid=%d\n", host.uid, host.gid))
	s.Contains(out, "checkout=abc123\n")
	s.Contains(out, focusedSum(focusedTestBinary)+"  ./e2e.test\n")
	s.Contains(out, focusedSum(focusedTestAgent)+"  ./bin/devsy-linux-amd64\n")
	s.Contains(out, "focused check "+focusedTestName+" passed in one attempt\n")
}

func (s *focusedCheckSuite) TestWrongUIDAbortsBeforeAnyCommand() {
	opts, _ := s.options("linux")
	host := s.passingHost()
	host.uid = 1000
	check := s.check
	check.RequireUID = 1001
	s.installHost(host)
	recorder := newFocusedRecorder()

	err := runFocusedChecks(context.Background(), recorder, focusedTestSuite(check), opts)

	s.ErrorIs(err, errFocusedUID)
	s.Empty(recorder.calls)
}

func (s *focusedCheckSuite) TestReportFailurePropagates() {
	opts, _ := s.options("linux")
	host := s.passingHost()
	s.installHost(host)
	recorder := newFocusedRecorder()
	recorder.report = focusedReport(
		false,
		true,
		`{"LeafNodeType":"It","LeafNodeText":"`+focusedTestLeaf+`","State":"passed","NumAttempts":2}`,
	)

	err := runFocusedChecks(
		context.Background(),
		recorder,
		focusedTestSuite(s.checkFor(host)),
		opts,
	)

	s.ErrorIs(err, errReportRetried)
	s.Len(recorder.calls, 4)
}

func (s *focusedCheckSuite) TestE2ERunFailurePropagates() {
	opts, _ := s.options("linux")
	host := s.passingHost()
	s.installHost(host)
	recorder := newFocusedRecorder()
	recorder.runErr = errors.New("e2e exploded")

	err := runFocusedChecks(
		context.Background(),
		recorder,
		focusedTestSuite(s.checkFor(host)),
		opts,
	)

	s.ErrorContains(err, "e2e exploded")
	s.Len(recorder.calls, 4)
}

func (s *focusedCheckSuite) TestNonLinuxSkipsPreflight() {
	opts, _ := s.options("darwin")
	host := s.passingHost()
	host.uid = 1000
	host.lookupUser = focusedLookupUserFails
	host.lookupGroup = focusedLookupGroupFails
	s.installHost(host)
	check := s.check
	check.RequireUID = 1001
	recorder := newFocusedRecorder()

	s.Require().
		NoError(runFocusedChecks(context.Background(), recorder, focusedTestSuite(check), opts))
	s.Len(recorder.calls, 4)
}

func (s *focusedCheckSuite) TestPreflight() {
	for _, tc := range s.preflightCases() {
		s.Run(tc.name, func() {
			host := s.passingHost()
			check := s.checkFor(host)
			opts, stdout := s.options("linux")
			if tc.mutate != nil {
				tc.mutate(&host, &check, &opts)
			}

			err := host.preflight(check, opts)

			if tc.wantErr == nil {
				s.Require().NoError(err)
				s.Contains(stdout.String(), fmt.Sprintf("uid=%d gid=%d\n", host.uid, host.gid))
				return
			}
			s.ErrorIs(err, tc.wantErr)
		})
	}
}

func (s *focusedCheckSuite) TestRequireWritableDirLeavesNoFiles() {
	dir := s.T().TempDir()

	s.Require().NoError(requireWritableDir(dir))

	entries, err := os.ReadDir(dir)
	s.Require().NoError(err)
	s.Empty(entries)
}

func (s *focusedCheckSuite) TestRequireOwnedBy() {
	dir := s.T().TempDir()
	s.Require().NoError(requireOwnedBy(dir, os.Getuid()))

	if runtime.GOOS == osWindows {
		s.Require().NoError(requireOwnedBy(dir, 1), "ownership is not checked on Windows")
		return
	}
	s.ErrorIs(requireOwnedBy(dir, os.Getuid()+1), errFocusedOwner)
}

func (s *focusedCheckSuite) TestPrintChecksumsSkipsMissingFiles() {
	opts, stdout := s.options("linux")
	s.Require().NoError(os.Remove(filepath.Join(opts.E2EDir, "bin", "devsy-linux-amd64")))

	s.Require().NoError(printChecksums(opts))

	s.Equal(focusedSum(focusedTestBinary)+"  ./e2e.test\n", stdout.String())
}

func (s *focusedCheckSuite) TestVerifyFocusedReport() {
	for _, tc := range s.reportCases() {
		s.Run(tc.name, func() {
			path := s.writeFile("report.json")
			s.Require().NoError(os.WriteFile(path, []byte(tc.report), 0o600))

			err := verifyFocusedReport(path, s.check)

			if tc.wantErr == nil {
				s.Require().NoError(err)
				return
			}
			s.ErrorIs(err, tc.wantErr)
		})
	}
}

func (s *focusedCheckSuite) TestVerifyFocusedReportMissingFile() {
	err := verifyFocusedReport(filepath.Join(s.T().TempDir(), "absent.json"), s.check)

	s.ErrorIs(err, errReportRead)
}

// preflightCases lists the host and directory conditions the preflight must reject or accept.
func (s *focusedCheckSuite) preflightCases() []focusedPreflightCase {
	return []focusedPreflightCase{
		{name: "passes for the current identity"},
		{
			name: "uid differs from the required uid",
			mutate: func(h *focusedHost, c *FocusedCheck, _ *e2eOptions) {
				h.uid = 1000
				c.RequireUID = 1001
			},
			wantErr: errFocusedUID,
		},
		{
			name: "gid differs from the required gid",
			mutate: func(h *focusedHost, c *FocusedCheck, _ *e2eOptions) {
				h.gid = 1000
				c.RequireGID = 1001
			},
			wantErr: errFocusedGID,
		},
		{
			name: "user does not resolve",
			mutate: func(h *focusedHost, _ *FocusedCheck, _ *e2eOptions) {
				h.lookupUser = focusedLookupUserFails
			},
			wantErr: errFocusedAccount,
		},
		{
			name: "group does not resolve",
			mutate: func(h *focusedHost, _ *FocusedCheck, _ *e2eOptions) {
				h.lookupGroup = focusedLookupGroupFails
			},
			wantErr: errFocusedAccount,
		},
		{
			name: "home is missing",
			mutate: func(h *focusedHost, _ *FocusedCheck, _ *e2eOptions) {
				h.home = filepath.Join(h.home, "missing")
			},
			wantErr: errFocusedDir,
		},
		{
			name: "home is a file",
			mutate: func(h *focusedHost, _ *FocusedCheck, _ *e2eOptions) {
				h.home = s.writeFile("home-file")
			},
			wantErr: errFocusedDir,
		},
		{
			name: "temp dir is missing",
			mutate: func(_ *focusedHost, _ *FocusedCheck, o *e2eOptions) {
				o.TempDir = filepath.Join(o.TempDir, "missing")
			},
			wantErr: errFocusedDir,
		},
	}
}

// reportCases lists the Ginkgo JSON reports the verifier must accept or reject.
func (s *focusedCheckSuite) reportCases() []focusedReportCase {
	return append(s.reportShapeCases(), s.reportLeafCases()...)
}

// reportShapeCases lists the reports that are malformed or describe the wrong suite.
func (s *focusedCheckSuite) reportShapeCases() []focusedReportCase {
	return []focusedReportCase{
		{
			name: "passes with non-It and skipped nodes ignored",
			report: focusedReport(
				false,
				true,
				focusedBeforeSuite,
				focusedLeafSkipped,
				focusedLeafPassed,
			),
		},
		{name: "not an array", report: `{}`, wantErr: errReportShape},
		{name: focusedMalformed, report: `[`, wantErr: errReportShape},
		{name: "empty array", report: `[]`, wantErr: errReportShape},
		{
			name: "two suite reports",
			report: "[" + focusedSuiteObject(false, true, focusedLeafPassed) + "," +
				focusedSuiteObject(false, true, focusedLeafPassed) + "]",
			wantErr: errReportShape,
		},
		{
			name:    "missing SuiteConfig",
			report:  `[{"SuiteSucceeded":true,"SpecReports":[` + focusedLeafPassed + `]}]`,
			wantErr: errReportShape,
		},
	}
}

// reportLeafCases lists the reports whose run status or selected spec is wrong.
func (s *focusedCheckSuite) reportLeafCases() []focusedReportCase {
	return []focusedReportCase{
		{
			name:    "dry run",
			report:  focusedReport(true, true, focusedLeafPassed),
			wantErr: errReportDryRun,
		},
		{
			name:    "suite failed",
			report:  focusedReport(false, false, focusedLeafPassed),
			wantErr: errReportSuite,
		},
		{
			name:    "zero executed leaves",
			report:  focusedReport(false, true),
			wantErr: errReportLeaves,
		},
		{
			name:    "only skipped leaf",
			report:  focusedReport(false, true, focusedLeafSkipped),
			wantErr: errReportLeaves,
		},
		{
			name:    "two executed leaves",
			report:  focusedReport(false, true, focusedLeafPassed, focusedLeafPassed),
			wantErr: errReportLeaves,
		},
		{
			name: "wrong spec text",
			report: focusedReport(
				false,
				true,
				`{"LeafNodeType":"It","LeafNodeText":"other spec","State":"passed","NumAttempts":1}`,
			),
			wantErr: errReportLeafText,
		},
		{
			name: "spec did not pass",
			report: focusedReport(
				false,
				true,
				`{"LeafNodeType":"It","LeafNodeText":"`+focusedTestLeaf+`","State":"failed","NumAttempts":1}`,
			),
			wantErr: errReportState,
		},
		{
			name: "spec retried",
			report: focusedReport(
				false,
				true,
				`{"LeafNodeType":"It","LeafNodeText":"`+focusedTestLeaf+`","State":"passed","NumAttempts":2}`,
			),
			wantErr: errReportRetried,
		},
	}
}

// options returns e2e options backed by a temp directory holding e2e.test and bin/devsy-linux-amd64.
func (s *focusedCheckSuite) options(goos string) (e2eOptions, *bytes.Buffer) {
	dir := s.T().TempDir()
	s.Require().NoError(os.MkdirAll(filepath.Join(dir, "bin"), 0o750))
	s.Require().
		NoError(os.WriteFile(filepath.Join(dir, "e2e.test"), []byte(focusedTestBinary), 0o600))
	s.Require().
		NoError(os.WriteFile(filepath.Join(dir, "bin", "devsy-linux-amd64"), []byte(focusedTestAgent), 0o600))
	stdout := &bytes.Buffer{}
	opts := e2eOptions{
		E2EDir:  dir,
		TempDir: s.T().TempDir(),
		GOOS:    goos,
		Stdout:  stdout,
		Stderr:  io.Discard,
	}
	return opts, stdout
}

// passingHost is the current process identity with a temp HOME and successful account lookups.
func (s *focusedCheckSuite) passingHost() focusedHost {
	return focusedHost{
		uid:  os.Getuid(),
		gid:  os.Getgid(),
		home: s.T().TempDir(),
		lookupUser: func(id string) (*user.User, error) {
			return &user.User{Uid: id}, nil
		},
		lookupGroup: func(id string) (*user.Group, error) {
			return &user.Group{Gid: id}, nil
		},
	}
}

// checkFor returns the suite's check requiring the identity of host.
func (s *focusedCheckSuite) checkFor(host focusedHost) FocusedCheck {
	check := s.check
	check.RequireUID = host.uid
	check.RequireGID = host.gid
	return check
}

// installHost makes host the focused-check host until the test ends.
func (s *focusedCheckSuite) installHost(host focusedHost) {
	previous := focusedHostProvider
	focusedHostProvider = func() focusedHost { return host }
	s.T().Cleanup(func() { focusedHostProvider = previous })
}

func (s *focusedCheckSuite) writeFile(name string) string {
	path := filepath.Join(s.T().TempDir(), name)
	s.Require().NoError(os.WriteFile(path, []byte("x"), 0o600))
	return path
}
