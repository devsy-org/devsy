// Package secretstore supplies command-owned interactive unlock behavior.
package secretstore

import (
	"context"
	"os"
	"sync"

	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/survey"
	"golang.org/x/term"
)

type invocationResolver struct {
	base     secrets.DefaultUnlockResolver
	material secrets.UnlockMaterial
	mu       sync.Mutex
}

func Options() secrets.StoreOptions {
	resolver := &invocationResolver{
		base: secrets.DefaultUnlockResolver{
			Prompt: func(_ context.Context, _ secrets.UnlockRequest) (string, error) {
				if !term.IsTerminal(int(os.Stdin.Fd())) { // #nosec G115 -- valid file descriptor.
					return "", &secrets.UnlockRequiredError{Backend: secrets.BackendFile}
				}
				return survey.NewSurvey().
					Question(&survey.QuestionOptions{Question: "Enter the secrets passphrase", IsPassword: true})
			},
		},
	}
	return secrets.StoreOptions{AllowPrompt: true, UnlockResolver: resolver}
}

func (r *invocationResolver) ResolvePassphrase(
	ctx context.Context,
	request secrets.UnlockRequest,
) (secrets.UnlockMaterial, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.material.Passphrase != "" {
		return r.material, nil
	}
	resolved, err := r.base.ResolvePassphrase(ctx, request)
	if err == nil {
		r.material = resolved
	}
	return resolved, err
}

// Material returns only material already needed by this invocation. Detached
// workers inherit it through their own environment without process-global writes.
func (r *invocationResolver) Material() (secrets.UnlockMaterial, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.material, r.material.Passphrase != ""
}
