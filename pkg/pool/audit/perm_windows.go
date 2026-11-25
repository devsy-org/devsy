//go:build windows

package audit

const noFollow = 0

func checkDirMode(string) error { return nil }
