//go:build darwin

package command

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func processTreeIdentity(pid int) (string, error) {
	sessionID, err := darwinSessionID(pid)
	if err != nil {
		if processLookupConfirmsExit(pid, err) {
			return "", nil
		}
		return "", err
	}
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if processLookupConfirmsExit(pid, err) {
			return "", nil
		}
		return "", err
	}
	return fmt.Sprintf(
		"%d:%d:%d",
		sessionID,
		process.Proc.P_starttime.Sec,
		process.Proc.P_starttime.Usec,
	), nil
}

func processLookupConfirmsExit(pid int, lookupErr error) bool {
	if !errors.Is(lookupErr, syscall.ESRCH) && !errors.Is(lookupErr, syscall.EIO) {
		return false
	}
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func processGroupMatchesIdentity(pgid int, identity string) (bool, error) {
	identityParts := strings.Split(identity, ":")
	if len(identityParts) != 3 {
		return false, fmt.Errorf("parse process tree identity %q", identity)
	}
	wantSession, err := strconv.Atoi(identityParts[0])
	if err != nil {
		return false, fmt.Errorf("parse session identity %q: %w", identity, err)
	}
	for _, part := range identityParts[1:] {
		if _, err := strconv.ParseInt(part, 10, 64); err != nil {
			return false, fmt.Errorf("parse process start identity %q: %w", identity, err)
		}
	}
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return false, err
	}
	return darwinProcessGroupHasSession(processes, pgid, wantSession, identity)
}

func darwinProcessGroupHasSession(
	processes []unix.KinfoProc,
	pgid, wantSession int,
	identity string,
) (bool, error) {
	foundMember := false
	for _, process := range processes {
		matches, leaderMismatch, err := darwinProcessGroupMemberMatches(
			process,
			pgid,
			wantSession,
			identity,
		)
		if err != nil {
			return false, err
		}
		if leaderMismatch {
			return false, nil
		}
		foundMember = foundMember || matches
	}
	return foundMember, nil
}

func darwinProcessGroupMemberMatches(
	process unix.KinfoProc,
	pgid, wantSession int,
	identity string,
) (matches, leaderMismatch bool, err error) {
	processGroupID := int(process.Eproc.Pgid)
	if processGroupID != pgid {
		return false, false, nil
	}
	pid := int(process.Proc.P_pid)
	sessionID, err := darwinSessionID(pid)
	if errors.Is(err, syscall.ESRCH) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if pid == pgid {
		leaderIdentity, err := processTreeIdentity(pid)
		if err != nil {
			return false, false, err
		}
		if leaderIdentity != "" && leaderIdentity != identity {
			return false, true, nil
		}
	}
	return sessionID == wantSession, false, nil
}

func darwinSessionID(pid int) (int, error) {
	return unix.Getsid(pid)
}
