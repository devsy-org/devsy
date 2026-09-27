//go:build linux

package command

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func processTreeIdentity(pid int) (string, error) {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	fields, ok := linuxProcessStatFields(stat)
	if !ok || len(fields) < 20 {
		return "", fmt.Errorf("read session identity for process %d", pid)
	}
	return fields[3] + ":" + fields[19], nil
}

func processGroupMatchesIdentity(pgid int, identity string) (bool, error) {
	wanted, err := parseLinuxProcessTreeIdentity(identity)
	if err != nil {
		return false, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}

	foundMember := false
	for _, entry := range entries {
		member, leaderMismatch, err := linuxProcessGroupEntryMatches(entry, pgid, wanted)
		if err != nil {
			return false, err
		}
		if leaderMismatch {
			return false, nil
		}
		foundMember = foundMember || member
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
		return linuxProcessTreeIdentity{}, fmt.Errorf("parse session identity %q: %w", identity, err)
	}
	wanted.startTime, err = strconv.ParseUint(identityParts[1], 10, 64)
	if err != nil {
		return linuxProcessTreeIdentity{}, fmt.Errorf("parse process start identity %q: %w", identity, err)
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
	stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, nil
		}
		return false, false, err
	}
	fields, ok := linuxProcessStatFields(stat)
	if !ok || len(fields) < 4 {
		return false, false, nil
	}
	processGroup, err := strconv.Atoi(fields[2])
	if err != nil || processGroup != pgid {
		return false, false, nil
	}
	sessionID, err := strconv.Atoi(fields[3])
	if err != nil {
		return false, false, nil
	}
	if pid == pgid {
		if len(fields) < 20 {
			return false, false, fmt.Errorf("read process start time for process %d", pid)
		}
		leaderStart, err := strconv.ParseUint(fields[19], 10, 64)
		if err != nil {
			return false, false, fmt.Errorf("parse process start time for process %d: %w", pid, err)
		}
		if sessionID != wanted.sessionID || leaderStart != wanted.startTime {
			return false, true, nil
		}
	}
	return sessionID == wanted.sessionID, false, nil
}

func linuxProcessStatFields(stat []byte) ([]string, bool) {
	statText := string(stat)
	closingParen := strings.LastIndexByte(statText, ')')
	if closingParen < 0 || closingParen+2 >= len(statText) {
		return nil, false
	}
	return strings.Fields(statText[closingParen+2:]), true
}
