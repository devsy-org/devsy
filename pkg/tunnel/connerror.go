package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

type ErrorKind int

const (
	ErrorTransient ErrorKind = iota
	ErrorPermanent
	ErrorShutdown
)

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

func IsEOF(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, io.EOF) || strings.Contains(err.Error(), ": EOF")
}

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
