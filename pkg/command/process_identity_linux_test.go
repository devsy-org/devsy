//go:build linux

package command

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestLinuxProcessGroupFieldsIgnoreExitedMembers(t *testing.T) {
	wanted := linuxProcessTreeIdentity{sessionID: 7, startTime: 42}
	for _, state := range []string{"Z", "X"} {
		member, mismatch, err := linuxProcessGroupFieldsMatch(
			12, 10, wanted, []string{state, "1", "10", "7"},
		)
		if err != nil || member || mismatch {
			t.Fatalf("state %s = (%t, %t, %v), want no live member", state, member, mismatch, err)
		}
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
	// #nosec G204,G702 -- run this test binary with a fixed test selector.
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

func fakeLinuxStats(stats map[int][]string, errs map[int]error) linuxStatReader {
	return func(pid int) ([]string, bool, error) {
		if err, ok := errs[pid]; ok {
			return nil, false, err
		}
		fields, ok := stats[pid]
		return fields, ok, nil
	}
}

const absentLinuxGroup = int(^uint32(0) >> 1)

type livingGroupCase struct {
	name    string
	pgid    int
	pids    []int
	stats   map[int][]string
	errs    map[int]error
	want    bool
	wantErr bool
}

func linuxLivingGroupCases(liveGroup int) []livingGroupCase {
	const absent = absentLinuxGroup
	denied := &os.PathError{Op: processStatOpenOperation, Err: os.ErrPermission}
	member := func(pgid int, state string) []string {
		return []string{state, "1", strconv.Itoa(pgid), strconv.Itoa(pgid)}
	}
	return []livingGroupCase{
		{
			name:  "readable live member wins over unreadable unrelated entry",
			pgid:  liveGroup,
			pids:  []int{10, 11},
			stats: map[int][]string{11: member(liveGroup, "S")},
			errs:  map[int]error{10: denied},
			want:  true,
		},
		{
			name:  "zombie-only group has no living members",
			pgid:  absent,
			pids:  []int{absent},
			stats: map[int][]string{absent: member(absent, "Z")},
		},
		{name: "vanished process is ignored", pgid: absent, pids: []int{10}},
		{
			name: "unreadable unrelated entry with absent group is complete",
			pgid: absent,
			pids: []int{10},
			errs: map[int]error{10: denied},
		},
		{
			name:    "unreadable unrelated entry with existing group fails closed",
			pgid:    liveGroup,
			pids:    []int{10},
			errs:    map[int]error{10: denied},
			wantErr: true,
		},
		{
			name:    "unreadable leader fails closed",
			pgid:    absent,
			pids:    []int{absent},
			errs:    map[int]error{absent: denied},
			wantErr: true,
		},
		{
			name:    "unexpected stat error fails closed",
			pgid:    absent,
			pids:    []int{10},
			errs:    map[int]error{10: syscall.EIO},
			wantErr: true,
		},
	}
}

func TestLinuxLivingGroupMembers(t *testing.T) {
	for _, tc := range linuxLivingGroupCases(startLinuxProcessGroupHelper(t)) {
		t.Run(tc.name, func(t *testing.T) {
			got, err := linuxLivingGroupMembers(tc.pids, tc.pgid, fakeLinuxStats(tc.stats, tc.errs))
			if tc.wantErr {
				require.Error(t, err)
				assert.False(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestProcessGroupHasLivingMembersRejectsNonpositiveGroup(t *testing.T) {
	for _, pgid := range []int{0, -1} {
		_, err := processGroupHasLivingMembers(pgid)
		require.Error(t, err)
	}
}
