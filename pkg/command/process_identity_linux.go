//go:build linux

package command

import (
	"fmt"
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
	identityParts := strings.Split(identity, ":")
	if len(identityParts) != 2 {
		return false, fmt.Errorf("parse process tree identity %q", identity)
	}
	wantSession, err := strconv.Atoi(identityParts[0])
	if err != nil {
		return false, fmt.Errorf("parse session identity %q: %w", identity, err)
	}
	wantStart, err := strconv.ParseUint(identityParts[1], 10, 64)
	if err != nil {
		return false, fmt.Errorf("parse process start identity %q: %w", identity, err)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}

	foundMember := false
	// A live group leader must still have the saved start time as well as the session ID.
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		processDir := filepath.Join("/proc", entry.Name())
		stat, err := os.ReadFile(filepath.Join(processDir, "stat"))
		if err != nil {
			if !os.IsNotExist(err) {
				return false, err
			}
			continue
		}
		fields, ok := linuxProcessStatFields(stat)
		if !ok || len(fields) < 4 {
			continue
		}
		processGroup, err := strconv.Atoi(fields[2])
		if err != nil || processGroup != pgid {
			continue
		}
		sessionID, err := strconv.Atoi(fields[3])
		if err != nil {
			continue
		}
		if pid == pgid {
			leaderStart, err := strconv.ParseUint(fields[19], 10, 64)
			if err != nil {
				return false, fmt.Errorf("parse process start time for process %d: %w", pid, err)
			}
			if sessionID != wantSession || leaderStart != wantStart {
				return false, nil
			}
		}
		if sessionID == wantSession {
			foundMember = true
		}
	}
	return foundMember, nil
}

func linuxProcessStatFields(stat []byte) ([]string, bool) {
	statText := string(stat)
	closingParen := strings.LastIndexByte(statText, ')')
	if closingParen < 0 || closingParen+2 >= len(statText) {
		return nil, false
	}
	return strings.Fields(statText[closingParen+2:]), true
}
