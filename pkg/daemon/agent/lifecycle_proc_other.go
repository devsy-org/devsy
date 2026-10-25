//go:build !linux

package agent

func procStartTicks(int) string { return "" }
