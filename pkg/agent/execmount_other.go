//go:build !linux

package agent

func dirAllowsExec(string) (bool, error) { return true, nil }
