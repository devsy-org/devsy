//go:build linux

package command

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const strongProcessIdentitySupported = true

func processTreeIdentity(pid int) (string, error) {
	fields, ok, err := linuxProcessStat(pid)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	if len(fields) < 20 {
		return "", fmt.Errorf("read session identity for process %d", pid)
	}
	return fields[3] + ":" + fields[19], nil
}

func processGroupMatchesIdentity(pgid int, identity string) (bool, error) {
	wanted, err := parseLinuxProcessTreeIdentity(identity)
	if err != nil {
		return false, err
	}
	leaderMismatch, err := linuxProcessGroupLeaderMismatch(pgid, wanted)
	if err != nil {
		return false, err
	}
	if leaderMismatch {
		return false, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	scan, err := linuxProcessGroupEntriesMatch(
		entries,
		pgid,
		wanted,
	)
	if err != nil {
		return false, err
	}
	if scan.leaderMismatch {
		return false, nil
	}
	return linuxProcessGroupMatchResult(pgid, scan.foundMember, scan.skippedStatErr)
}

func linuxProcessGroupLeaderMismatch(
	pgid int,
	wanted linuxProcessTreeIdentity,
) (bool, error) {
	fields, found, err := linuxProcessStat(pgid)
	if err != nil || !found {
		return false, err
	}
	_, mismatch, err := linuxProcessGroupFieldsMatch(pgid, pgid, wanted, fields)
	return mismatch, err
}

type linuxProcessGroupScan struct {
	foundMember    bool
	leaderMismatch bool
	skippedStatErr error
}

func linuxProcessGroupEntriesMatch(
	entries []os.DirEntry,
	pgid int,
	wanted linuxProcessTreeIdentity,
) (linuxProcessGroupScan, error) {
	var scan linuxProcessGroupScan
	for _, entry := range entries {
		member, mismatchedLeader, err := linuxProcessGroupEntryMatches(entry, pgid, wanted)
		if err != nil {
			if linuxProcessGroupEntryErrorCanBeSkipped(entry, pgid, err) {
				scan.skippedStatErr = err
				continue
			}
			return scan, err
		}
		if mismatchedLeader {
			scan.leaderMismatch = true
			return scan, nil
		}
		scan.foundMember = scan.foundMember || member
	}
	return scan, nil
}

func linuxProcessGroupEntryErrorCanBeSkipped(
	entry os.DirEntry,
	pgid int,
	err error,
) bool {
	pid, parseErr := strconv.Atoi(entry.Name())
	return parseErr == nil && linuxCanSkipProcessStatError(pid, pgid, err)
}

func linuxCanSkipProcessStatError(pid, pgid int, err error) bool {
	return pid != pgid && errors.Is(err, fs.ErrPermission)
}

func linuxProcessGroupMatchResult(pgid int, foundMember bool, skippedStatErr error) (bool, error) {
	if skippedStatErr != nil && !foundMember {
		if errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) {
			return false, nil
		}
		return false, fmt.Errorf(
			"verify process group %d after unreadable process stats: %w",
			pgid,
			skippedStatErr,
		)
	}
	return foundMember, nil
}

type linuxProcessTreeIdentity struct {
	sessionID int
	startTime uint64
}

func parseLinuxProcessTreeIdentity(identity string) (linuxProcessTreeIdentity, error) {
	identityParts := strings.Split(identity, ":")
	if len(identityParts) != 2 {
		return linuxProcessTreeIdentity{}, fmt.Errorf("parse process tree identity %q", identity)
	}
	wanted := linuxProcessTreeIdentity{}
	var err error
	wanted.sessionID, err = strconv.Atoi(identityParts[0])
	if err != nil {
		return linuxProcessTreeIdentity{}, fmt.Errorf(
			"parse session identity %q: %w",
			identity,
			err,
		)
	}
	wanted.startTime, err = strconv.ParseUint(identityParts[1], 10, 64)
	if err != nil {
		return linuxProcessTreeIdentity{}, fmt.Errorf(
			"parse process start identity %q: %w",
			identity,
			err,
		)
	}
	return wanted, nil
}

// A live group leader must still have the saved start time as well as the session ID.
func linuxProcessGroupEntryMatches(
	entry fs.DirEntry,
	pgid int,
	wanted linuxProcessTreeIdentity,
) (member, leaderMismatch bool, err error) {
	pid, err := strconv.Atoi(entry.Name())
	if err != nil {
		return false, false, nil
	}
	fields, ok, err := linuxProcessStat(pid)
	if err != nil {
		return false, false, err
	}
	if !ok || len(fields) < 4 {
		return false, false, nil
	}
	return linuxProcessGroupFieldsMatch(pid, pgid, wanted, fields)
}

func linuxProcessGroupFieldsMatch(
	pid, pgid int,
	wanted linuxProcessTreeIdentity,
	fields []string,
) (member, leaderMismatch bool, err error) {
	processGroup, sessionID, ok := linuxProcessGroupIDs(fields)
	if !ok || processGroup != pgid {
		return false, false, nil
	}
	if pid == pgid {
		leaderMismatch, err = linuxProcessGroupFieldsLeaderMismatch(pid, sessionID, fields, wanted)
	}
	if err != nil || leaderMismatch {
		return false, leaderMismatch, err
	}
	if fields[0] == "Z" || fields[0] == "X" {
		return false, false, nil
	}
	return sessionID == wanted.sessionID, false, nil
}

func linuxProcessGroupIDs(fields []string) (processGroup, sessionID int, ok bool) {
	if len(fields) < 4 {
		return 0, 0, false
	}
	processGroup, err := strconv.Atoi(fields[2])
	if err != nil {
		return 0, 0, false
	}
	sessionID, err = strconv.Atoi(fields[3])
	return processGroup, sessionID, err == nil
}

func linuxProcessGroupFieldsLeaderMismatch(
	pid, sessionID int,
	fields []string,
	wanted linuxProcessTreeIdentity,
) (bool, error) {
	matches, err := linuxProcessLeaderMatches(pid, sessionID, fields, wanted)
	return !matches, err
}

func linuxProcessStat(pid int) ([]string, bool, error) {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		if linuxProcessStatErrorIsAbsent(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	fields, ok := linuxProcessStatFields(stat)
	if !ok {
		return nil, true, fmt.Errorf("parse process stat for process %d", pid)
	}
	return fields, true, nil
}

func linuxProcessStatErrorIsAbsent(err error) bool {
	return errors.Is(err, syscall.ESRCH) || os.IsNotExist(err)
}

func linuxProcessLeaderMatches(
	pid, sessionID int,
	fields []string,
	wanted linuxProcessTreeIdentity,
) (bool, error) {
	if len(fields) < 20 {
		return false, fmt.Errorf("read process start time for process %d", pid)
	}
	leaderStart, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return false, fmt.Errorf("parse process start time for process %d: %w", pid, err)
	}
	return sessionID == wanted.sessionID && leaderStart == wanted.startTime, nil
}

func linuxProcessStatFields(stat []byte) ([]string, bool) {
	statText := string(stat)
	closingParen := strings.LastIndexByte(statText, ')')
	if closingParen < 0 || closingParen+2 >= len(statText) {
		return nil, false
	}
	return strings.Fields(statText[closingParen+2:]), true
}
