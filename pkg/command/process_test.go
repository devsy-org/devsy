//go:build linux || darwin || unix

package command

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const testSavedIdentity = "1:2:3"

func TestKillTerminatesRunningProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := strconv.Itoa(cmd.Process.Pid)

	if err := Kill(pid); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("process still running after Kill")
	}
}

func TestKillOnAlreadyExitedProcessIsNoop(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	pid := strconv.Itoa(cmd.Process.Pid)

	if err := Kill(pid); err != nil {
		t.Errorf("Kill on exited process = %v, want nil", err)
	}
}

func TestKillTreeTerminatesGroupAfterWorkerExits(t *testing.T) {
	dir := t.TempDir()
	childPIDFile := filepath.Join(dir, "child.pid")
	// #nosec G204 -- the fixed shell script and all paths are test-controlled.
	cmd := exec.Command(
		"sh",
		"-c",
		`trap '' TERM; sleep 60 >/dev/null 2>&1 & echo $! > "$1"; exit 0`,
		"sh",
		childPIDFile,
	)
	prepareBackgroundTree(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	workerPID := strconv.Itoa(cmd.Process.Pid)
	identity, err := ProcessTreeIdentity(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("process tree identity: %v", err)
	}
	t.Cleanup(func() { _ = killTreeAfterWorkerExit(workerPID, "detached-task", identity) })

	childPID := readTestPID(t, childPIDFile)

	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait for worker exit: %v", err)
	}
	waitForTestProcess(t, childPID, true)

	if err := killTreeAfterWorkerExit(workerPID, "detached-task", identity); err != nil {
		t.Fatalf("killTree after worker exit: %v", err)
	}
	waitForTestProcess(t, childPID, false)
}

func TestKillTreeAfterWorkerExitWithoutIdentityDoesNotSignalUnrelatedGroup(t *testing.T) {
	worker := startUnixTestProcess(t, &syscall.SysProcAttr{Setpgid: true})
	pid := strconv.Itoa(worker.Process.Pid)
	err := killTreeAfterWorkerExit(pid, "stale-task", "")
	if err == nil {
		t.Fatal("killTreeAfterWorkerExit without identity = nil for a live process group")
	}
	if !strings.Contains(err.Error(), "saved process identity is unavailable") {
		t.Fatalf("killTreeAfterWorkerExit error = %v, want missing-identity context", err)
	}
	if running, err := isRunning(pid); err != nil || !running {
		t.Fatalf("unidentified process running=%t, err=%v", running, err)
	}
}

func TestKillTreeAfterWorkerExitWithMismatchedIdentityDoesNotSignalUnrelatedGroup(t *testing.T) {
	worker := startUnixTestProcess(t, &syscall.SysProcAttr{Setpgid: true})
	pid := strconv.Itoa(worker.Process.Pid)

	otherSession := startUnixTestProcess(t, &syscall.SysProcAttr{Setsid: true})
	staleIdentity := processTreeIdentityForTest(t, otherSession.Process.Pid)
	workerIdentity := processTreeIdentityForTest(t, worker.Process.Pid)
	if staleIdentity == workerIdentity {
		t.Fatalf("separate sessions have identical process tree identity %q", workerIdentity)
	}

	if err := killTreeAfterWorkerExit(pid, "stale-task", staleIdentity); err != nil {
		t.Fatalf("killTreeAfterWorkerExit with stale identity: %v", err)
	}
	if running, err := isRunning(pid); err != nil || !running {
		t.Fatalf("unrelated process running=%t, err=%v", running, err)
	}
}

func TestKillTreeWithMismatchedSavedIdentityDoesNotSignalReusedProcessGroup(t *testing.T) {
	worker := startUnixTestProcess(t, &syscall.SysProcAttr{Setpgid: true})
	otherSession := startUnixTestProcess(t, &syscall.SysProcAttr{Setsid: true})
	staleIdentity := processTreeIdentityForTest(t, otherSession.Process.Pid)
	workerIdentity := processTreeIdentityForTest(t, worker.Process.Pid)
	if staleIdentity == workerIdentity {
		t.Fatalf("separate sessions have identical process tree identity %q", workerIdentity)
	}

	if err := killTreeWithIdentity(
		strconv.Itoa(worker.Process.Pid),
		"stale-task",
		staleIdentity,
	); err != nil {
		t.Fatalf("killTreeWithIdentity with stale identity: %v", err)
	}
	if running, err := isRunning(strconv.Itoa(worker.Process.Pid)); err != nil || !running {
		t.Fatalf("reused process running=%t, err=%v", running, err)
	}
}

func TestKillTreeIdentityRejectsDifferentGroupLeaderStartTime(t *testing.T) {
	worker := startUnixTestProcess(t, &syscall.SysProcAttr{Setpgid: true})
	workerIdentity := processTreeIdentityForTest(t, worker.Process.Pid)
	identityParts := strings.Split(workerIdentity, ":")
	if len(identityParts) < 2 {
		t.Fatalf("unexpected process tree identity %q", workerIdentity)
	}
	startTime, err := strconv.ParseInt(identityParts[len(identityParts)-1], 10, 64)
	if err != nil {
		t.Fatalf("parse process start identity: %v", err)
	}
	identityParts[len(identityParts)-1] = strconv.FormatInt(startTime+1, 10)
	staleIdentity := strings.Join(identityParts, ":")

	matches, err := processGroupMatchesIdentity(worker.Process.Pid, staleIdentity)
	if err != nil {
		t.Fatalf("processGroupMatchesIdentity: %v", err)
	}
	if matches {
		t.Fatal("process group matched another process start identity in the same session")
	}
	if err := killTreeWithIdentity(
		strconv.Itoa(worker.Process.Pid),
		"stale-task",
		staleIdentity,
	); err != nil {
		t.Fatalf("killTreeWithIdentity with stale start identity: %v", err)
	}
	if running, err := isRunning(strconv.Itoa(worker.Process.Pid)); err != nil || !running {
		t.Fatalf("worker running=%t, err=%v", running, err)
	}
}

func TestKillTreeWithMissingIdentityDoesNotSignalWorker(t *testing.T) {
	worker := startUnixTestProcess(t, &syscall.SysProcAttr{Setpgid: true})
	pid := strconv.Itoa(worker.Process.Pid)
	if err := killTreeWithIdentity(pid, "task", ""); err == nil {
		t.Fatal("killTreeWithIdentity without saved identity = nil for a live worker")
	}
	if running, err := isRunning(pid); err != nil || !running {
		t.Fatalf("worker running=%t, err=%v", running, err)
	}
}

func TestProcessTreeIdentityMatchesDetachedSession(t *testing.T) {
	worker := exec.Command("sleep", "30")
	worker.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := worker.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	defer func() { _ = worker.Process.Kill() }()

	identity, err := ProcessTreeIdentity(worker.Process.Pid)
	if err != nil {
		t.Fatalf("process tree identity: %v", err)
	}
	matches, err := processGroupMatchesIdentity(worker.Process.Pid, identity)
	if err != nil {
		t.Fatalf("match process group identity: %v", err)
	}
	if !matches {
		t.Fatal("process group does not belong to its detached session")
	}
}

func startUnixTestProcess(t *testing.T, attr *syscall.SysProcAttr) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = attr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start test process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

func processTreeIdentityForTest(t *testing.T, pid int) string {
	t.Helper()
	identity, err := ProcessTreeIdentity(pid)
	if err != nil {
		t.Fatalf("read process tree identity: %v", err)
	}
	return identity
}

func readTestPID(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// #nosec G304 -- this test reads only its own temporary PID file.
		data, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("worker did not publish PID in %s", path)
	return ""
}

func waitForTestProcess(t *testing.T, pid string, wantRunning bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		running, err := isRunning(pid)
		if err != nil {
			t.Fatalf("isRunning(%s): %v", pid, err)
		}
		if running == wantRunning {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %s running=%t, want %t", pid, !wantRunning, wantRunning)
}

func TestKillInvalidPIDReturnsError(t *testing.T) {
	for _, pid := range []string{"not-a-pid", "0", "-5"} {
		if _, err := IsRunning(pid); err == nil {
			t.Errorf("IsRunning(%q) = nil error, want rejection", pid)
		}
		if err := Kill(pid); err == nil {
			t.Errorf("Kill(%q) = nil, want error", pid)
		}
	}
}

func TestTerminateUnidentifiedProcessGroup(t *testing.T) {
	cmd := startSigtermIgnoringGroup(t)
	pid := cmd.Process.Pid
	ref := ProcessRef{PID: pid, TreeKind: ProcessTreeLegacyPID}
	err := terminateProcessRef(ref)
	if !strongProcessIdentitySupported {
		if err == nil {
			t.Fatal("identityless platform signaled a group it could not verify")
		}
		if running, _ := isRunning(strconv.Itoa(pid)); !running {
			t.Fatal("identityless platform terminated an unidentified worker")
		}
		return
	}
	if err != nil {
		t.Fatalf("terminateProcessRef: %v", err)
	}
	waitForNoLivingGroupMembers(t, pid)
}

func TestCleanupExitedIdentitylessGroupReportsSurvivingDescendants(t *testing.T) {
	pid := startUnixTestProcess(t, &syscall.SysProcAttr{Setpgid: true}).Process.Pid
	ref := ProcessRef{
		PID:      pid,
		TreeKind: ProcessTreeUnixGroup,
		TreeID:   strconv.Itoa(pid),
	}

	err := cleanupExitedProcessRef(ref)
	if err == nil {
		t.Fatal("cleanupExitedProcessRef = nil for an identityless group with survivors")
	}
	if !strings.Contains(err.Error(), "no saved identity") {
		t.Fatalf("cleanupExitedProcessRef error = %v, want missing-identity context", err)
	}
	if running, _ := isRunning(strconv.Itoa(pid)); !running {
		t.Fatal("cleanupExitedProcessRef terminated an unidentified group")
	}
}

func startSigtermIgnoringGroup(t *testing.T) *exec.Cmd {
	t.Helper()
	ready := filepath.Join(t.TempDir(), "ready")
	// #nosec G204 -- the fixed shell script and all paths are test-controlled.
	cmd := exec.Command("sh", "-c", `trap '' TERM; printf 'ready\n' > "$1"; sleep 30`, "sh", ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start SIGTERM-ignoring group: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		// #nosec G304 -- this test reads only its own temporary readiness file.
		data, err := os.ReadFile(ready)
		if err == nil && string(data) == "ready\n" {
			return cmd
		}
		if time.Now().After(deadline) {
			t.Fatalf("SIGTERM-ignoring group did not report readiness: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForNoLivingGroupMembers(t *testing.T, pgid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		live, err := processGroupHasLivingMembers(pgid)
		if err != nil {
			t.Fatalf("check living members of group %d: %v", pgid, err)
		}
		if !live {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker group %d still has living members after termination", pgid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestKillWorkerTreeWithStaleIdentityLeavesLiveLeaderRunning(t *testing.T) {
	pid := startUnixTestProcess(t, &syscall.SysProcAttr{Setpgid: true}).Process.Pid
	identity := processTreeIdentityForTest(t, pid)
	stale := identity[:strings.LastIndex(identity, ":")] + ":0"
	if err := killWorkerTreeWithIdentity(pid, "detached-task", stale); err != nil {
		t.Fatalf("killWorkerTreeWithIdentity with stale identity: %v", err)
	}
	if running, err := isRunning(strconv.Itoa(pid)); err != nil || !running {
		t.Fatalf("live leader with stale identity: running=%t err=%v, want running", running, err)
	}
}

func TestConfirmGroupEPERMKeepsLiveOrUnverifiedGroupErrors(t *testing.T) {
	matchErr := errors.New("enumerate processes")
	tests := []struct {
		name     string
		identity string
		matcher  func(int, string) (bool, error)
		wantNil  bool
		wantErrs []error
	}{
		{
			name:     "live matching member keeps EPERM",
			identity: testSavedIdentity,
			matcher:  func(int, string) (bool, error) { return true, nil },
			wantErrs: []error{syscall.EPERM},
		},
		{
			name:     "failed verification keeps EPERM and verification error",
			identity: testSavedIdentity,
			matcher:  func(int, string) (bool, error) { return false, matchErr },
			wantErrs: []error{syscall.EPERM, matchErr},
		},
		{
			name:     "no matching member confirms completion",
			identity: testSavedIdentity,
			matcher:  func(int, string) (bool, error) { return false, nil },
			wantNil:  true,
		},
		{
			name:     "missing identity never confirms completion",
			identity: "",
			matcher:  func(int, string) (bool, error) { return false, nil },
			wantErrs: []error{syscall.EPERM},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := confirmGroupEPERM(1234, tc.identity, syscall.EPERM, tc.matcher)
			if tc.wantNil {
				if err != nil {
					t.Fatalf("confirmGroupEPERM = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("confirmGroupEPERM = nil, want error")
			}
			for _, want := range tc.wantErrs {
				if !errors.Is(err, want) {
					t.Fatalf("confirmGroupEPERM = %v, want errors.Is %v", err, want)
				}
			}
		})
	}
}

var (
	errSignalUnexpected = errors.New("unexpected")
	errSignalVerify     = errors.New("enumerate processes")
)

type groupMatcher func(int, string) (bool, error)

func matchesLiveMember(int, string) (bool, error) { return true, nil }
func matchesNoMember(int, string) (bool, error)   { return false, nil }
func matchFails(int, string) (bool, error)        { return false, errSignalVerify }

type signalResultCase struct {
	name     string
	target   int
	killErr  error
	identity string
	matcher  groupMatcher
	want     signalOutcome
	wantErrs []error
}

func groupCase(name string, killErr error, matcher groupMatcher) signalResultCase {
	return signalResultCase{
		name:     name,
		target:   -42,
		killErr:  killErr,
		identity: testSavedIdentity,
		matcher:  matcher,
	}
}

func (c signalResultCase) onPositiveTarget() signalResultCase {
	c.target = 42
	return c
}

func (c signalResultCase) expect(want signalOutcome, wantErrs ...error) signalResultCase {
	c.want = want
	c.wantErrs = wantErrs
	return c
}

func signalResultCases() []signalResultCase {
	return []signalResultCase{
		{name: "signal delivered", target: 42, want: signalSent},
		groupCase("positive target gone", syscall.ESRCH, nil).
			onPositiveTarget().expect(targetGone),
		groupCase("group gone", syscall.ESRCH, nil).expect(targetGone),
		groupCase("group EPERM without live member is gone", syscall.EPERM, matchesNoMember).
			expect(targetGone),
		groupCase("group EPERM with live member keeps error", syscall.EPERM, matchesLiveMember).
			expect(signalSent, syscall.EPERM),
		groupCase("group EPERM with failed verification keeps both", syscall.EPERM, matchFails).
			expect(signalSent, syscall.EPERM, errSignalVerify),
		groupCase("positive target EPERM is never confirmed", syscall.EPERM, matchesNoMember).
			onPositiveTarget().expect(signalSent, syscall.EPERM),
		groupCase("unexpected error is returned", errSignalUnexpected, matchesNoMember).
			expect(signalSent, errSignalUnexpected),
	}
}

func TestClassifySignalResult(t *testing.T) {
	for _, tc := range signalResultCases() {
		t.Run(tc.name, func(t *testing.T) {
			got, err := classifySignalResult(tc.target, tc.killErr, tc.identity, tc.matcher)
			if got != tc.want {
				t.Fatalf("outcome = %v, want %v", got, tc.want)
			}
			if len(tc.wantErrs) == 0 && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			for _, want := range tc.wantErrs {
				if !errors.Is(err, want) {
					t.Fatalf("err = %v, want errors.Is %v", err, want)
				}
			}
		})
	}
}

func TestSignalProcessTreeReturnsImmediatelyWhenTargetAlreadyGone(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run short-lived process: %v", err)
	}
	start := time.Now()
	if err := signalProcessTree(cmd.Process.Pid, true, testSavedIdentity); err != nil {
		t.Fatalf("signalProcessTree for exited process: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("signalProcessTree took %v for an already-exited target", elapsed)
	}
}
