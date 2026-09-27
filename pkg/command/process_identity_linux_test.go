//go:build linux

package command

import (
	"os"
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
		name          string
		pid           int
		pgid          int
		leaderMatches bool
		want          bool
	}{
		{name: "unrelated process with verified leader", pid: 12, pgid: 7, leaderMatches: true, want: true},
		{name: "worker leader is unreadable", pid: 7, pgid: 7, leaderMatches: true},
		{name: "leader exited", pid: 12, pgid: 7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := linuxCanSkipProcessStatError(test.pid, test.pgid, test.leaderMatches, permissionErr); got != test.want {
				t.Fatalf("linuxCanSkipProcessStatError() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestLinuxProcessGroupMatchRequiresAMemberWhenStatsAreUnreadable(t *testing.T) {
	permissionErr := &os.PathError{Op: processStatOpenOperation, Err: os.ErrPermission}
	if matched, err := linuxProcessGroupMatchResult(7, true, permissionErr); err != nil || !matched {
		t.Fatalf("verified group with unreadable unrelated process = (%t, %v), want (true, nil)", matched, err)
	}
	if matched, err := linuxProcessGroupMatchResult(7, false, permissionErr); err == nil || matched {
		t.Fatalf("unverified group with unreadable process = (%t, %v), want error", matched, err)
	}
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
