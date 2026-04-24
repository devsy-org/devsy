package framework

import (
	"fmt"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exitError runs a shell command that exits with the given code and returns
// the resulting *exec.ExitError.
func exitError(t *testing.T, code string) *exec.ExitError {
	t.Helper()
	// #nosec G204 -- test helper with controlled exit code argument
	err := exec.Command("sh", "-c", "exit "+code).Run()
	require.Error(t, err)
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr
}

func TestIsRetryableSSHError_ExitStatus1(t *testing.T) {
	assert.True(t, isRetryableSSHError(exitError(t, "1")))
}

func TestIsRetryableSSHError_ExitStatus10(t *testing.T) {
	assert.False(t, isRetryableSSHError(exitError(t, "10")))
}

func TestIsRetryableSSHError_ExitStatus127(t *testing.T) {
	assert.False(t, isRetryableSSHError(exitError(t, "127")))
}

func TestIsRetryableSSHError_Nil(t *testing.T) {
	assert.False(t, isRetryableSSHError(nil))
}

func TestIsRetryableSSHError_NonExitError(t *testing.T) {
	assert.False(t, isRetryableSSHError(fmt.Errorf("some other error")))
}

func TestIsRetryableDockerError_RateLimit(t *testing.T) {
	stderr := `GET https://index.docker.io/v2/library/ubuntu/manifests/latest: ` +
		`TOOMANYREQUESTS: You have reached your unauthenticated pull rate limit.`
	assert.True(t, isRetryableDockerError(stderr))
}

func TestIsRetryableDockerError_Timeout(t *testing.T) {
	stderr := `Get "https://registry-1.docker.io/v2/": net/http: TLS handshake timeout`
	assert.True(t, isRetryableDockerError(stderr))
}

func TestIsRetryableDockerError_IOTimeout(t *testing.T) {
	stderr := `Get "https://registry-1.docker.io/v2/library/ubuntu/manifests/latest": i/o timeout`
	assert.True(t, isRetryableDockerError(stderr))
}

func TestIsRetryableDockerError_ConnectionReset(t *testing.T) {
	stderr := `error pulling image: read tcp 10.0.0.1:443: read: connection reset by peer`
	assert.True(t, isRetryableDockerError(stderr))
}

func TestIsRetryableDockerError_ServiceUnavailable(t *testing.T) {
	stderr := `received unexpected HTTP status: 503 Service Unavailable`
	assert.True(t, isRetryableDockerError(stderr))
}

func TestIsRetryableDockerError_RealFailure(t *testing.T) {
	stderr := `error resolving dockerfile: dockerfile not found`
	assert.False(t, isRetryableDockerError(stderr))
}

func TestIsRetryableDockerError_Empty(t *testing.T) {
	assert.False(t, isRetryableDockerError(""))
}
