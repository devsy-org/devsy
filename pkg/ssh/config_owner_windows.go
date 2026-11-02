//go:build windows

package ssh

import "os"

func preserveConfigOwner(_ string, _ *os.File) {}
