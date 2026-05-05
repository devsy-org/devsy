package feature

import (
	"errors"
	"net/http"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"k8s.io/apimachinery/pkg/util/wait"
)

var ociBackoff = wait.Backoff{
	Duration: 2 * time.Second,
	Factor:   2.0,
	Steps:    4,
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	var terr *transport.Error
	if errors.As(err, &terr) {
		return terr.StatusCode >= http.StatusInternalServerError ||
			terr.StatusCode == http.StatusTooManyRequests ||
			terr.StatusCode == http.StatusRequestTimeout
	}

	return true
}

func retryOCIPull(fn func() error) error {
	var lastErr error
	err := wait.ExponentialBackoff(ociBackoff, func() (bool, error) {
		lastErr = fn()
		if lastErr == nil {
			return true, nil
		}
		if !isTransientError(lastErr) {
			return false, lastErr
		}
		log.Debugf("OCI pull transient failure: %v", lastErr)
		return false, nil
	})
	if wait.Interrupted(err) {
		return lastErr
	}
	return err
}
