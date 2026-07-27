package docker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
)

var buildBackoff = wait.Backoff{
	Duration: 2 * time.Second,
	Factor:   2.0,
	Steps:    3,
}

// errTransientBuild marks a build failure that is safe to retry.
var errTransientBuild = errors.New("transient build failure")

// transientBuildPatterns are lowercase substrings matched against the build
// error and output. Add a pattern when a new transient failure is observed.
var transientBuildPatterns = []string{
	"incompleteread",
}

// classifyBuildError wraps err with errTransientBuild when it matches a
// known-transient signature. Context cancellation is never transient.
func classifyBuildError(err error, output string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	haystack := strings.ToLower(err.Error() + "\n" + output)
	for _, pattern := range transientBuildPatterns {
		if strings.Contains(haystack, pattern) {
			return fmt.Errorf("%w: %w", errTransientBuild, err)
		}
	}
	return err
}
