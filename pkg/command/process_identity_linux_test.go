//go:build linux

package command

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
)

const processStatOpenOperation = "open"

func TestLinuxProcessStatErrorIsAbsent(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "process disappeared",
			err:  &os.PathError{Op: processStatOpenOperation, Err: syscall.ESRCH},
			want: true,
		},
		{
			name: "missing proc entry",
			err:  &os.PathError{Op: processStatOpenOperation, Err: os.ErrNotExist},
			want: true,
		},
		{
			name: "permission denied",
			err:  &os.PathError{Op: processStatOpenOperation, Err: os.ErrPermission},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := linuxProcessStatErrorIsAbsent(test.err); got != test.want {
				t.Fatalf(
					"linuxProcessStatErrorIsAbsent(%v) = %t, want %t",
					test.err,
					got,
					test.want,
				)
			}
		})
	}
}

func TestLinuxCanSkipUnrelatedUnreadableProcess(t *testing.T) {
	permissionErr := &os.PathError{Op: processStatOpenOperation, Err: os.ErrPermission}
	tests := []struct {
		name string
		pid  int
		pgid int
		want bool
	}{
		{
			name: "unrelated process with verified leader",
			pid:  12,
			pgid: 7,
			want: true,
		},
		{name: "worker leader is unreadable", pid: 7, pgid: 7},
		{name: "leader exited", pid: 12, pgid: 7, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := linuxCanSkipProcessStatError(
				test.pid,
				test.pgid,
				permissionErr,
			)
			if got != test.want {
				t.Fatalf("linuxCanSkipProcessStatError() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestLinuxProcessGroupMatchRequiresAMemberWhenStatsAreUnreadable(t *testing.T) {
	permissionErr := &os.PathError{Op: processStatOpenOperation, Err: os.ErrPermission}
	pgid := startLinuxProcessGroupHelper(t)
	matched, err := linuxProcessGroupMatchResult(pgid, true, permissionErr)
	if err != nil || !matched {
		t.Fatalf(
			"verified group with unreadable unrelated process = (%t, %v), want (true, nil)",
			matched,
			err,
		)
	}
	matched, err = linuxProcessGroupMatchResult(pgid, false, permissionErr)
	if err == nil || matched {
		t.Fatalf("unverified group with unreadable process = (%t, %v), want error", matched, err)
	}
}

func TestLinuxProcessGroupMatchTreatsMissingGroupAsGone(t *testing.T) {
	permissionErr := &os.PathError{Op: processStatOpenOperation, Err: os.ErrPermission}
	const largestPID = int(^uint32(0) >> 1)

	matched, err := linuxProcessGroupMatchResult(largestPID, false, permissionErr)
	if err != nil || matched {
		t.Fatalf("missing process group = (%t, %v), want (false, nil)", matched, err)
	}
}

func TestLinuxProcessGroupProbeHelper(t *testing.T) {
	if os.Getenv("DEVSY_PROCESS_GROUP_HELPER") != "1" {
		return
	}
	select {}
}

func startLinuxProcessGroupHelper(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLinuxProcessGroupProbeHelper$")
	cmd.Env = append(os.Environ(), "DEVSY_PROCESS_GROUP_HELPER=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start process group helper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

func TestProcessTreeIdentityForMissingProcess(t *testing.T) {
	identity, err := processTreeIdentity(int(^uint(0) >> 1))
	if err != nil {
		t.Fatalf("processTreeIdentity for missing PID: %v", err)
	}
	if identity != "" {
		t.Fatalf("processTreeIdentity for missing PID = %q, want empty identity", identity)
	}
}
