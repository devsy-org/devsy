package config

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// SystemHostInfo provides real system resource information.
type SystemHostInfo struct {
	WorkspacePath string
}

func (s SystemHostInfo) NumCPU() int {
	return runtime.NumCPU()
}

func (s SystemHostInfo) TotalMemoryBytes() (uint64, error) {
	return readMemTotalFromProc()
}

func (s SystemHostInfo) AvailableStorageBytes(path string) (uint64, error) {
	if path == "" {
		path = "/"
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("statfs %q: %w", path, err)
	}
	return stat.Bavail * uint64(stat.Bsize), nil //nolint:gosec // Bsize type varies by platform
}

func readMemTotalFromProc() (uint64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("open /proc/meminfo: %w", err)
	}
	defer f.Close() //nolint:errcheck // best-effort close on read-only file

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("unexpected MemTotal format: %q", line)
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse MemTotal: %w", err)
		}
		return kb * 1024, nil
	}
	return 0, fmt.Errorf("MemTotal not found in /proc/meminfo")
}
