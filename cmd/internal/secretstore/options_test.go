package secretstore

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPurpose = "unlock"

// newTestResolver mirrors the base resolver Options() builds, with the process
// environment and remembered-passphrase store pinned so the host cannot leak in.
func newTestResolver(
	prompt func(context.Context, secrets.UnlockRequest) (string, error),
) *invocationResolver {
	return &invocationResolver{
		base: secrets.DefaultUnlockResolver{
			LookupEnv:      func(string) string { return "" },
			ReadRemembered: func() (string, error) { return "", nil },
			Prompt:         prompt,
		},
	}
}

func TestInvocationResolverReusesResolvedMaterial(t *testing.T) {
	var prompts atomic.Int32
	resolver := newTestResolver(func(context.Context, secrets.UnlockRequest) (string, error) {
		prompts.Add(1)
		return "typed-once", nil
	})

	first, err := resolver.ResolvePassphrase(
		context.Background(),
		secrets.UnlockRequest{AllowPrompt: true, Purpose: testPurpose},
	)
	require.NoError(t, err)
	second, err := resolver.ResolvePassphrase(
		context.Background(),
		secrets.UnlockRequest{Purpose: testPurpose},
	)
	require.NoError(t, err)

	assert.Equal(
		t,
		secrets.UnlockMaterial{Passphrase: "typed-once", Source: secrets.PassphrasePrompt},
		first,
	)
	assert.Equal(t, first, second)
	assert.Equal(t, int32(1), prompts.Load())
}

func TestInvocationResolverRetriesAfterFailedPrompt(t *testing.T) {
	promptErr := errors.New("prompt cancelled")
	prompts := 0
	resolver := newTestResolver(func(context.Context, secrets.UnlockRequest) (string, error) {
		prompts++
		if prompts == 1 {
			return "", promptErr
		}
		return "retry-value", nil
	})
	request := secrets.UnlockRequest{AllowPrompt: true, Purpose: testPurpose}

	_, err := resolver.ResolvePassphrase(context.Background(), request)
	require.ErrorIs(t, err, promptErr)
	_, ok := resolver.Material()
	assert.False(t, ok, "failed resolution must not be reported as material")

	material, err := resolver.ResolvePassphrase(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, "retry-value", material.Passphrase)
	assert.Equal(t, 2, prompts)
}

func TestInvocationResolverMaterialIsEmptyUntilResolved(t *testing.T) {
	prompts := 0
	resolver := newTestResolver(func(context.Context, secrets.UnlockRequest) (string, error) {
		prompts++
		return "typed", nil
	})

	material, ok := resolver.Material()
	assert.False(t, ok)
	assert.Empty(t, material.Passphrase)
	assert.Equal(t, 0, prompts, "reading material must never prompt")

	_, err := resolver.ResolvePassphrase(
		context.Background(),
		secrets.UnlockRequest{AllowPrompt: true, Purpose: testPurpose},
	)
	require.NoError(t, err)

	material, ok = resolver.Material()
	assert.True(t, ok)
	assert.Equal(t, "typed", material.Passphrase)
	assert.Equal(t, secrets.PassphrasePrompt, material.Source)
}

// parkedCallers counts goroutines blocked on the resolver mutex inside
// ResolvePassphrase. Callers cannot be observed entering the method directly, so
// their goroutine stacks are inspected instead.
func parkedCallers() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for stack := range strings.SplitSeq(string(buf), "\n\n") {
		if strings.Contains(stack, "(*invocationResolver).ResolvePassphrase") &&
			strings.Contains(stack, "Mutex).Lock") {
			count++
		}
	}
	return count
}

func waitForParkedCallers(want int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for parkedCallers() < want {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
	return true
}

func TestInvocationResolverPromptsOnceForConcurrentCallers(t *testing.T) {
	const callers = 16
	var prompts atomic.Int32
	var contended atomic.Bool
	resolver := newTestResolver(func(context.Context, secrets.UnlockRequest) (string, error) {
		prompts.Add(1)
		// Hold the prompt until every other caller is parked on the mutex, so the
		// first resolution cannot finish before all callers have contended.
		contended.Store(waitForParkedCallers(callers-1, 5*time.Second))
		return "shared", nil
	})
	request := secrets.UnlockRequest{AllowPrompt: true, Purpose: testPurpose}

	var wg sync.WaitGroup
	results := make([]secrets.UnlockMaterial, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Go(func() {
			results[i], errs[i] = resolver.ResolvePassphrase(context.Background(), request)
		})
	}
	wg.Wait()

	require.True(t, contended.Load(), "callers did not all contend on the resolver")
	for i := range callers {
		require.NoError(t, errs[i])
		assert.Equal(t, "shared", results[i].Passphrase)
	}
	assert.Equal(t, int32(1), prompts.Load())
}
