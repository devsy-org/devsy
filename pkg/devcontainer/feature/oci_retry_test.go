package feature

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/stretchr/testify/suite"
)

type OCIRetryTestSuite struct {
	suite.Suite
}

func TestOCIRetryTestSuite(t *testing.T) {
	suite.Run(t, new(OCIRetryTestSuite))
}

func (s *OCIRetryTestSuite) TestRetrySucceedsOnThirdAttempt() {
	attempts := 0
	err := retryOCIPull(func() error {
		attempts++
		if attempts < 3 {
			return fmt.Errorf("connection reset")
		}
		return nil
	})

	s.NoError(err)
	s.Equal(3, attempts)
}

func (s *OCIRetryTestSuite) TestRetryGivesUpAfterMaxAttempts() {
	attempts := 0
	err := retryOCIPull(func() error {
		attempts++
		return fmt.Errorf("persistent network error")
	})

	s.Error(err)
	s.Equal(ociMaxRetries, attempts)
	s.Contains(err.Error(), "persistent network error")
}

func (s *OCIRetryTestSuite) TestNoRetryOn4xx() {
	attempts := 0
	err := retryOCIPull(func() error {
		attempts++
		return &transport.Error{StatusCode: http.StatusNotFound}
	})

	s.Error(err)
	s.Equal(1, attempts)
}

func (s *OCIRetryTestSuite) TestNoRetryOn401() {
	attempts := 0
	err := retryOCIPull(func() error {
		attempts++
		return &transport.Error{StatusCode: http.StatusUnauthorized}
	})

	s.Error(err)
	s.Equal(1, attempts)
}

func (s *OCIRetryTestSuite) TestRetryOn5xx() {
	attempts := 0
	err := retryOCIPull(func() error {
		attempts++
		if attempts < 3 {
			return &transport.Error{StatusCode: http.StatusServiceUnavailable}
		}
		return nil
	})

	s.NoError(err)
	s.Equal(3, attempts)
}

func (s *OCIRetryTestSuite) TestSucceedsFirstTry() {
	attempts := 0
	err := retryOCIPull(func() error {
		attempts++
		return nil
	})

	s.NoError(err)
	s.Equal(1, attempts)
}

func (s *OCIRetryTestSuite) TestExponentialBackoff() {
	attempts := 0
	var timestamps []time.Time
	err := retryOCIPull(func() error {
		timestamps = append(timestamps, time.Now())
		attempts++
		if attempts < 3 {
			return fmt.Errorf("timeout")
		}
		return nil
	})

	s.NoError(err)
	s.Require().Len(timestamps, 3)

	firstGap := timestamps[1].Sub(timestamps[0])
	secondGap := timestamps[2].Sub(timestamps[1])

	s.GreaterOrEqual(firstGap.Milliseconds(), int64(900))
	s.GreaterOrEqual(secondGap.Milliseconds(), int64(1800))
}

func (s *OCIRetryTestSuite) TestIsTransientError_Nil() {
	s.False(isTransientError(nil))
}

func (s *OCIRetryTestSuite) TestIsTransientError_5xx() {
	err := &transport.Error{StatusCode: http.StatusInternalServerError}
	s.True(isTransientError(err))
}

func (s *OCIRetryTestSuite) TestIsTransientError_4xx() {
	err := &transport.Error{StatusCode: http.StatusForbidden}
	s.False(isTransientError(err))
}

func (s *OCIRetryTestSuite) TestIsTransientError_NetworkError() {
	err := fmt.Errorf("dial tcp: connection refused")
	s.True(isTransientError(err))
}
