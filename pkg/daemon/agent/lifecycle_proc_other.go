//go:build !linux

package agent

func procStartTicks(int) string { return "" }

func processStartIdentity(int) (string, error) { return "", nil }

func sameProcess(pid int, startTicks string) bool {
	return processExists(pid) && (startTicks == "" || procStartTicks(pid) == startTicks)
}
