package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrorKind classifies connection errors.
type ErrorKind int

const (
	// ErrorTransient indicates a temporary error that may resolve on retry.
	ErrorTransient ErrorKind = iota
	// ErrorPermanent indicates an unrecoverable error.
	ErrorPermanent
	// ErrorShutdown indicates a graceful or context-driven shutdown.
	ErrorShutdown
)

// ClassifyError returns the kind of a connection error.
func ClassifyError(err error) ErrorKind {
	if err == nil {
		return ErrorShutdown
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrorShutdown
	}
	if IsEOF(err) {
		return ErrorTransient
	}
	return ErrorPermanent
}

// IsEOF reports whether err is caused by an EOF condition,
// including wrapped SSH handshake failures from closed pipes.
func IsEOF(err error) bool {
	return errors.Is(err, io.EOF) || strings.Contains(err.Error(), ": EOF")
}

// ClassifyTunnelErrors determines which error to report when tunnel and/or
// handler goroutines fail. EOF errors from the handler are suppressed when
// the tunnel error is the root cause.
func ClassifyTunnelErrors(tunnelErr, handlerErr error) error {
	if handlerErr == nil {
		return nil
	}
	if IsEOF(handlerErr) {
		if tunnelErr != nil {
			return fmt.Errorf("connect to server: %w", tunnelErr)
		}
		return nil
	}
	return fmt.Errorf("tunnel to container: %w", handlerErr)
}
