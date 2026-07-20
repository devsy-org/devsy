package selfupdate

import (
	stderrs "errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/google/go-github/v89/github"
)

func TestClassifyGitHubError_RateLimit(t *testing.T) {
	rl := &github.RateLimitError{
		Message:  "API rate limit exceeded",
		Response: &http.Response{StatusCode: http.StatusForbidden},
	}
	err := classifyGitHubError(fmt.Errorf("list releases for o/r: %w", rl))

	if !stderrs.Is(err, clierr.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
	var rlErr *github.RateLimitError
	if !stderrs.As(err, &rlErr) {
		t.Fatal("original *github.RateLimitError should remain in the chain")
	}
}

func TestClassifyGitHubError_Passthrough(t *testing.T) {
	orig := fmt.Errorf("some other failure")
	if got := classifyGitHubError(orig); got != orig {
		t.Fatalf("non-rate-limit error should pass through unchanged; got %v", got)
	}
	if classifyGitHubError(nil) != nil {
		t.Fatal("nil should pass through as nil")
	}
}
