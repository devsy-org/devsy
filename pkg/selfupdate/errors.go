package selfupdate

import (
	"errors"
	"fmt"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/google/go-github/v90/github"
)

func classifyGitHubError(err error) error {
	if err == nil {
		return nil
	}
	var (
		rateLimit *github.RateLimitError
		abuse     *github.AbuseRateLimitError
	)
	if errors.As(err, &rateLimit) || errors.As(err, &abuse) {
		return fmt.Errorf("%w: %w", clierr.ErrRateLimited, err)
	}
	return err
}
