package feature

import (
	"errors"
	"net/http"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

const (
	ociMaxRetries    = 3
	ociBaseDelay     = 1 * time.Second
	ociRetryExponent = 2
)

// isTransientError returns true for errors that may resolve on retry:
// network timeouts, connection resets, and 5xx server errors.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	var terr *transport.Error
	if errors.As(err, &terr) {
		return terr.StatusCode >= http.StatusInternalServerError
	}

	return true
}

// retryOCIPull executes fn up to ociMaxRetries times with exponential backoff.
// It only retries when isTransientError returns true.
func retryOCIPull(fn func() error) error {
	var lastErr error
	delay := ociBaseDelay

	for attempt := range ociMaxRetries {
		if attempt > 0 {
			log.Debugf("OCI pull retry: attempt=%d, delay=%v", attempt+1, delay)
			time.Sleep(delay)
			delay *= ociRetryExponent
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if !isTransientError(lastErr) {
			return lastErr
		}

		log.Debugf("OCI pull transient failure: attempt=%d, error=%v", attempt+1, lastErr)
	}

	return lastErr
}
