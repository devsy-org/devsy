//go:build windows

package command

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/devsy-org/devsy/pkg/config"
	"golang.org/x/sys/windows"
)

const (
	helperEnvMarker         = "DEVSY_TEST_HELPER_PROCESS"
	helperEnvPIDFile        = "DEVSY_TEST_HELPER_PIDFILE"
	helperEnvChildPIDFile   = "DEVSY_TEST_HELPER_CHILD_PIDFILE"
	helperEnvWaitFile       = "DEVSY_TEST_HELPER_WAIT_FILE"
	helperEnvExitAfterSpawn = "DEVSY_TEST_HELPER_EXIT_AFTER_SPAWN"
	helperEnvExitCode       = "DEVSY_TEST_HELPER_EXIT_CODE"
	helperEnvCheckJob       = "DEVSY_TEST_HELPER_CHECK_JOB"
)

var procIsProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

// TestHelperProcess is not a test; every helper process re-executes the test
// binary into it.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnvMarker) != "1" {
		return
	}
	if exitCode := os.Getenv(helperEnvExitCode); exitCode != "" {
		code, err := strconv.Atoi(exitCode)
		if err != nil {
			os.Exit(2)
		}
		os.Exit(code)
	}
	if jobResult := os.Getenv(helperEnvCheckJob); jobResult != "" {
		inJob, err := currentProcessInJob()
		if err != nil {
			os.Exit(3)
		}
		if err := os.WriteFile(jobResult, []byte(strconv.FormatBool(inJob)), 0o600); err != nil {
			os.Exit(4)
		}
	}
	if waitFile := os.Getenv(helperEnvWaitFile); waitFile != "" {
		for {
			if _, err := os.Stat(waitFile); err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if pidFile := os.Getenv(helperEnvPIDFile); pidFile != "" {
		child := exec.Command(os.Args[0], "-test.run", "TestHelperProcess")
		child.Env = helperEnv(helperEnvMarker + "=1")
		if grandchildPIDFile := os.Getenv(helperEnvChildPIDFile); grandchildPIDFile != "" {
			child.Env = append(child.Env,
				helperEnvPIDFile+"="+grandchildPIDFile,
				helperEnvExitAfterSpawn+"=1",
			)
		}
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
		if err := os.WriteFile(
			pidFile,
			[]byte(strconv.Itoa(child.Process.Pid)),
			0o600,
		); err != nil {
			os.Exit(1)
		}
		if os.Getenv(helperEnvExitAfterSpawn) == "1" {
			os.Exit(0)
		}
	}
	for {
		time.Sleep(time.Hour)
	}
}

// helperEnv strips helper markers from the inherited environment; Windows
// process creation needs the rest (e.g. SYSTEMROOT) intact.
func helperEnv(extra ...string) []string {
	env := []string{}
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "DEVSY_TEST_HELPER_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

func startHelper(t *testing.T, extraEnv ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "TestHelperProcess")
	cmd.Env = helperEnv(append([]string{helperEnvMarker + "=1"}, extraEnv...)...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

func assertRunning(t *testing.T, pid int) {
	t.Helper()
	running, err := isRunning(strconv.Itoa(pid))
	if err != nil {
		t.Fatalf("isRunning(%d): %v", pid, err)
	}
	if !running {
		t.Fatalf("isRunning(%d) = false, want true", pid)
	}
}

func assertNotRunningEventually(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		running, err := isRunning(strconv.Itoa(pid))
		if err == nil && !running {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("process %d still running after 15s", pid)
}

func TestIsRunningDetectsLiveAndExitedProcess(t *testing.T) {
	cmd := startHelper(t)
	assertRunning(t, cmd.Process.Pid)

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	_ = cmd.Wait()
	assertNotRunningEventually(t, cmd.Process.Pid)
}

func TestIsRunningRecognizesExitCode259AsExited(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run", "TestHelperProcess")
	cmd.Env = helperEnv(helperEnvMarker+"=1", helperEnvExitCode+"=259")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	processHandle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("open helper process: %v", err)
	}
	defer func() { _ = windows.CloseHandle(processHandle) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	result, err := windows.WaitForSingleObject(processHandle, 10_000)
	if err != nil {
		t.Fatalf("wait for helper: %v", err)
	}
	if result != windows.WAIT_OBJECT_0 {
		t.Fatalf("wait result = %#x, want WAIT_OBJECT_0", result)
	}

	running, err := isRunning(strconv.Itoa(cmd.Process.Pid))
	if err != nil {
		t.Fatalf("isRunning(%d): %v", cmd.Process.Pid, err)
	}
	if running {
		t.Fatal("isRunning returned true for exited process with exit code 259")
	}

	if err := cmd.Wait(); err == nil || cmd.ProcessState.ExitCode() != 259 {
		t.Fatalf("helper exit = %v (%v), want exit code 259", err, cmd.ProcessState)
	}
}

func TestStartDetachedAssignsWorkerToJobBeforeItRuns(t *testing.T) {
	dir := t.TempDir()
	jobName := "devsy-test-suspended-worker"
	jobResult := filepath.Join(dir, "in-job")
	pidFile := filepath.Join(dir, "worker.pid")
	cmd := exec.Command(os.Args[0], "-test.run", "TestHelperProcess")
	cmd.Env = helperEnv(helperEnvMarker+"=1", helperEnvCheckJob+"="+jobResult)

	if err := startDetached(cmd, jobName, pidFile, filepath.Join(dir, "streams")); err != nil {
		t.Fatalf("startDetached: %v", err)
	}
	pid := waitForPIDFile(t, pidFile)
	t.Cleanup(func() { _ = KillTree(strconv.Itoa(pid), jobName) })
	job, found, err := openWorkerJob(jobName)
	if err != nil || !found {
		t.Fatalf("open worker job after launcher exit: found=%t, err=%v", found, err)
	}
	_ = windows.CloseHandle(job)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(jobResult); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, err := os.ReadFile(jobResult) // #nosec G304 -- test-controlled path
	if err != nil {
		t.Fatalf("read job membership result: %v", err)
	}
	if strings.TrimSpace(string(data)) != "true" {
		t.Fatalf("worker job membership = %q, want true", data)
	}
}

func TestKillTreeWithIdentityDoesNotFallbackToUnverifiedPID(t *testing.T) {
	worker := startHelper(t)
	jobName := "devsy-test-no-job-" + strconv.Itoa(worker.Process.Pid)
	identity, err := ProcessTreeIdentity(worker.Process.Pid)
	if err != nil {
		t.Fatalf("ProcessTreeIdentity: %v", err)
	}
	if identity == "" {
		t.Fatal("ProcessTreeIdentity returned empty identity for live worker")
	}
	if err := killTreeWithIdentity(
		strconv.Itoa(worker.Process.Pid),
		jobName,
		identity,
	); err == nil {
		t.Fatal("killTreeWithIdentity without a job object = nil for a live worker")
	}
	if running, err := isRunning(strconv.Itoa(worker.Process.Pid)); err != nil || !running {
		t.Fatalf("worker running=%t, err=%v", running, err)
	}
}

func TestKillTreeAfterWorkerExitWithoutIdentityFailsClosedWhenJobIsMissing(t *testing.T) {
	worker := startHelper(t)
	if err := worker.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	_ = worker.Wait()

	err := killTreeAfterWorkerExit(strconv.Itoa(worker.Process.Pid), "devsy-up-abcdefghijkl", "")
	if err == nil ||
		!strings.Contains(err.Error(), "cannot verify descendants without process identity") {
		t.Fatalf("killTreeAfterWorkerExit error = %v, want fail-closed identity error", err)
	}
}

func TestOwnProcessTreeRejectsExistingJobObject(t *testing.T) {
	workerName := "devsy-test-existing-job-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	jobName, err := jobNameFor(workerName)
	if err != nil {
		t.Fatalf("resolve job name: %v", err)
	}
	name, err := windows.UTF16PtrFromString(jobName)
	if err != nil {
		t.Fatalf("convert job name: %v", err)
	}
	existingJob, err := windows.CreateJobObject(nil, name)
	if err != nil {
		t.Fatalf("create existing job object: %v", err)
	}
	defer func() { _ = windows.CloseHandle(existingJob) }()

	err = ownProcessTree(0, workerName)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("ownProcessTree with an existing job = %v, want already-exists error", err)
	}
}

func TestKillTreeWithIdentityFindsLegacyJobObject(t *testing.T) {
	worker := startHelper(t)
	workerName := "devsy-test-legacy-job-" + strconv.Itoa(worker.Process.Pid)
	name, err := windows.UTF16PtrFromString(workerName)
	if err != nil {
		t.Fatalf("convert legacy job name: %v", err)
	}
	job, err := windows.CreateJobObject(nil, name)
	if err != nil {
		t.Fatalf("create legacy job: %v", err)
	}
	defer func() { _ = windows.CloseHandle(job) }()

	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(worker.Process.Pid),
	)
	if err != nil {
		t.Fatalf("open helper process: %v", err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		t.Fatalf("assign helper to legacy job: %v", err)
	}
	identity, err := ProcessTreeIdentity(worker.Process.Pid)
	if err != nil {
		t.Fatalf("ProcessTreeIdentity: %v", err)
	}
	if err := killTreeWithIdentity(
		strconv.Itoa(worker.Process.Pid),
		workerName,
		identity,
	); err != nil {
		t.Fatalf("killTreeWithIdentity: %v", err)
	}
	assertNotRunningEventually(t, worker.Process.Pid)
}

func TestKillTreeFindsLegacyJobObject(t *testing.T) {
	worker := startHelper(t)
	workerName := "devsy-test-legacy-cancel-" + strconv.Itoa(worker.Process.Pid)
	name, err := windows.UTF16PtrFromString(workerName)
	if err != nil {
		t.Fatalf("convert legacy job name: %v", err)
	}
	job, err := windows.CreateJobObject(nil, name)
	if err != nil {
		t.Fatalf("create legacy job: %v", err)
	}
	defer func() { _ = windows.CloseHandle(job) }()
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(worker.Process.Pid),
	)
	if err != nil {
		t.Fatalf("open helper process: %v", err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		t.Fatalf("assign helper to legacy job: %v", err)
	}

	if err := KillTree(strconv.Itoa(worker.Process.Pid), workerName); err != nil {
		t.Fatalf("KillTree: %v", err)
	}
	assertNotRunningEventually(t, worker.Process.Pid)
}

func TestKillTreeAfterWorkerExitFindsLegacyDetachedJob(t *testing.T) {
	worker := startHelper(t)
	workerName := "devsy-up-abcdefghijkl"
	name, err := windows.UTF16PtrFromString(workerName)
	if err != nil {
		t.Fatalf("convert legacy job name: %v", err)
	}
	job, err := windows.CreateJobObject(nil, name)
	if err != nil {
		t.Fatalf("create legacy job: %v", err)
	}
	defer func() { _ = windows.CloseHandle(job) }()

	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(worker.Process.Pid),
	)
	if err != nil {
		t.Fatalf("open helper process: %v", err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		t.Fatalf("assign helper to legacy job: %v", err)
	}
	terminated, err := terminateJobAfterWorkerExit(workerName)
	if err != nil {
		t.Fatalf("terminateJobAfterWorkerExit: %v", err)
	}
	if !terminated {
		t.Fatal("terminateJobAfterWorkerExit found no legacy job")
	}
	assertNotRunningEventually(t, worker.Process.Pid)
}

func TestJobNameForSeparatesDevsyHomes(t *testing.T) {
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)

	t.Setenv(config.EnvHome, t.TempDir())
	first, err := jobNameFor("jupyter")
	if err != nil {
		t.Fatalf("jobNameFor first home: %v", err)
	}

	t.Setenv(config.EnvHome, t.TempDir())
	second, err := jobNameFor("jupyter")
	if err != nil {
		t.Fatalf("jobNameFor second home: %v", err)
	}
	if first == second {
		t.Fatalf("job names collide across Devsy homes: %q", first)
	}
}

func TestJobNameForCanonicalizesEquivalentHomePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	first, err := jobNameFor("jupyter")
	if err != nil {
		t.Fatalf("jobNameFor first path: %v", err)
	}

	t.Setenv(config.EnvHome, strings.ToUpper(home))
	second, err := jobNameFor("jupyter")
	if err != nil {
		t.Fatalf("jobNameFor second path: %v", err)
	}
	if first != second {
		t.Fatalf("equivalent home paths have different job names: %q != %q", first, second)
	}
}

func TestJobNameForDoesNotRequireHomeDirectoryAtCancellation(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatalf("create home: %v", err)
	}
	t.Setenv(config.EnvHome, home)
	first, err := jobNameFor("jupyter")
	if err != nil {
		t.Fatalf("jobNameFor first call: %v", err)
	}
	if err := os.Remove(home); err != nil {
		t.Fatalf("remove home: %v", err)
	}
	second, err := jobNameFor("jupyter")
	if err != nil {
		t.Fatalf("jobNameFor second call: %v", err)
	}
	if first != second {
		t.Fatalf("missing home produced different job names: %q != %q", first, second)
	}
}

func TestTerminateJobForPIDFindsLegacyJobWhenScopedJobIsUnrelated(t *testing.T) {
	worker := startHelper(t)
	workerName := "devsy-test-legacy-fallback-" + strconv.Itoa(worker.Process.Pid)
	scopedName, err := jobNameFor(workerName)
	if err != nil {
		t.Fatalf("resolve scoped job name: %v", err)
	}
	scopedNamePtr, err := windows.UTF16PtrFromString(scopedName)
	if err != nil {
		t.Fatalf("convert scoped job name: %v", err)
	}
	scopedJob, err := windows.CreateJobObject(nil, scopedNamePtr)
	if err != nil {
		t.Fatalf("create scoped job: %v", err)
	}
	defer func() { _ = windows.CloseHandle(scopedJob) }()

	legacyNamePtr, err := windows.UTF16PtrFromString(workerName)
	if err != nil {
		t.Fatalf("convert legacy job name: %v", err)
	}
	legacyJob, err := windows.CreateJobObject(nil, legacyNamePtr)
	if err != nil {
		t.Fatalf("create legacy job: %v", err)
	}
	defer func() { _ = windows.CloseHandle(legacyJob) }()
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(worker.Process.Pid),
	)
	if err != nil {
		t.Fatalf("open helper process: %v", err)
	}
	defer func() { _ = windows.CloseHandle(process) }()
	if err := windows.AssignProcessToJobObject(legacyJob, process); err != nil {
		t.Fatalf("assign helper to legacy job: %v", err)
	}

	terminated, err := terminateJobForPID(workerName, worker.Process.Pid)
	if err != nil {
		t.Fatalf("terminateJobForPID: %v", err)
	}
	if !terminated {
		t.Fatal("terminateJobForPID found no matching job")
	}
	assertNotRunningEventually(t, worker.Process.Pid)
}

func currentProcessInJob() (bool, error) {
	var inJob int32
	result, _, callErr := procIsProcessInJob.Call(
		uintptr(windows.CurrentProcess()),
		0,
		uintptr(unsafe.Pointer(&inJob)),
	)
	if result == 0 {
		return false, callErr
	}
	return inJob != 0, nil
}

func waitForPIDFile(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile) // #nosec G304 -- test-controlled path
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no PID reported in %s", pidFile)
	return 0
}

func TestKillTerminatesProcessTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	parent := startHelper(t, helperEnvPIDFile+"="+pidFile)

	childPID := waitForPIDFile(t, pidFile)

	assertRunning(t, parent.Process.Pid)
	assertRunning(t, childPID)

	if err := Kill(strconv.Itoa(parent.Process.Pid)); err != nil {
		t.Fatalf("Kill(parent): %v", err)
	}

	assertNotRunningEventually(t, parent.Process.Pid)
	assertNotRunningEventually(t, childPID)
}

func TestKillOnAlreadyExitedProcessIsNoop(t *testing.T) {
	cmd := startHelper(t)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	_ = cmd.Wait()
	assertNotRunningEventually(t, cmd.Process.Pid)

	if err := Kill(strconv.Itoa(cmd.Process.Pid)); err != nil {
		t.Errorf("Kill on exited process = %v, want nil", err)
	}
}

func TestKillIsIdempotent(t *testing.T) {
	cmd := startHelper(t)
	pid := strconv.Itoa(cmd.Process.Pid)

	if err := Kill(pid); err != nil {
		t.Fatalf("first Kill: %v", err)
	}
	if err := Kill(pid); err != nil {
		t.Errorf("second Kill = %v, want nil", err)
	}
}

func TestInvalidPIDReturnsError(t *testing.T) {
	for _, pid := range []string{"not-a-pid", "0", "-5"} {
		if _, err := isRunning(pid); err == nil {
			t.Errorf("isRunning(%q) = nil error, want rejection", pid)
		}
		if err := killTree(pid, ""); err == nil {
			t.Errorf("kill(%q) = nil, want error", pid)
		}
	}
}

func TestKillTerminatesOrphanedGrandchildViaJobObject(t *testing.T) {
	dir := t.TempDir()
	waitFile := filepath.Join(dir, "go")
	childPIDFile := filepath.Join(dir, "child.pid")
	grandchildPIDFile := filepath.Join(dir, "grandchild.pid")

	// The child spawns the grandchild and exits, orphaning the grandchild
	// from the perspective of parent-based termination.
	parentPIDFile := filepath.Join(dir, "parent.pid")
	parent := exec.Command(os.Args[0], "-test.run", "TestHelperProcess")
	parent.Env = helperEnv(
		helperEnvMarker+"=1",
		helperEnvWaitFile+"="+waitFile,
		helperEnvPIDFile+"="+childPIDFile,
		helperEnvChildPIDFile+"="+grandchildPIDFile,
		helperEnvExitAfterSpawn+"=1",
	)
	if err := startDetached(
		parent,
		"devsy-test-worker",
		parentPIDFile,
		filepath.Join(dir, "streams"),
	); err != nil {
		t.Fatalf("startDetached: %v", err)
	}
	parentPID := waitForPIDFile(t, parentPIDFile)
	identity, err := ProcessTreeIdentity(parentPID)
	if err != nil {
		t.Fatalf("ProcessTreeIdentity: %v", err)
	}
	if identity == "" {
		t.Fatalf("ProcessTreeIdentity(%d) returned an empty identity", parentPID)
	}
	t.Cleanup(
		func() { _ = KillTreeAfterWorkerExit(strconv.Itoa(parentPID), "devsy-test-worker", identity) },
	)
	if err := os.WriteFile(waitFile, []byte("go"), 0o600); err != nil {
		t.Fatalf("signal helper: %v", err)
	}

	childPID := waitForPIDFile(t, childPIDFile)
	grandchildPID := waitForPIDFile(t, grandchildPIDFile)
	assertNotRunningEventually(t, childPID)
	assertNotRunningEventually(t, parentPID)
	assertNotRunningEventually(t, grandchildPID)
	if err := killTreeAfterWorkerExit(
		strconv.Itoa(parentPID),
		"devsy-test-worker",
		identity,
	); err != nil {
		t.Fatalf("KillTreeAfterWorkerExit after natural tree cleanup: %v", err)
	}
}
