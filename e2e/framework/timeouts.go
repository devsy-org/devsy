package framework

import (
	"runtime"
	"time"
)

const osWindows = "windows"

func TimeoutShort() time.Duration {
	if runtime.GOOS == osWindows {
		return 10 * time.Minute
	}
	return 3 * time.Minute
}

func TimeoutModerate() time.Duration {
	if runtime.GOOS == osWindows {
		return 25 * time.Minute
	}
	return 5 * time.Minute
}

func TimeoutLong() time.Duration {
	if runtime.GOOS == osWindows {
		return 50 * time.Minute
	}
	return 10 * time.Minute
}

func TimeoutVeryLong() time.Duration {
	if runtime.GOOS == osWindows {
		return 100 * time.Minute
	}
	return 20 * time.Minute
}
