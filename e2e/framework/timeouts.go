package framework

import (
	"time"
)

func TimeoutShort() time.Duration {
	return 3 * time.Minute
}

func TimeoutModerate() time.Duration {
	return 5 * time.Minute
}

func TimeoutLong() time.Duration {
	return 10 * time.Minute
}

func TimeoutVeryLong() time.Duration {
	return 20 * time.Minute
}
