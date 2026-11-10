package framework

import (
	"runtime"
	"sync/atomic"
)

type Framework struct {
	DevsyBinDir       string
	DevsyBinName      string
	liveStdoutDropped atomic.Int64
	liveStderrDropped atomic.Int64
}

func NewDefaultFramework(path string) *Framework {
	binName := "devsy-"
	switch runtime.GOOS {
	case "darwin":
		binName += "darwin-"
	case "linux":
		binName += "linux-"
	case osWindows:
		binName += "windows-"
	}

	switch runtime.GOARCH {
	case "amd64":
		binName += "amd64"
	case "arm64":
		binName += "arm64"
	}

	if runtime.GOOS == osWindows {
		binName += ".exe"
	}

	return &Framework{DevsyBinDir: path, DevsyBinName: binName}
}

func (f *Framework) LiveOutputDropped() (int64, int64) {
	return f.liveStdoutDropped.Load(), f.liveStderrDropped.Load()
}
