package docker

import (
	"context"
	"fmt"
	"slices"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/session"
)

const verbStart = "start"

func notWorkspace(r provider.Resource) bool {
	role, labelled := r.Labels[provider.LabelRole]
	return labelled && role != provider.RoleWorkspace
}

// Start starts the session's workspace container. Docker accepts a start of a
// container that already runs, which is what makes Start idempotent. It takes
// the session lock, so it never runs against a Spawn or Reclaim of the same
// session.
func (p *Provider) Start(ctx context.Context, pool, sessionID string) error {
	if err := checkPool(ctx, pool); err != nil {
		return err
	}
	if !session.OwnsID(pool, sessionID) {
		return provider.ErrInvalidSession
	}
	unlock, err := p.locks.lock(ctx, sessionID)
	if err != nil {
		return err
	}
	defer unlock()
	found, err := p.find(ctx, kinds[:1], scope{pool: pool, session: sessionID})
	if err != nil {
		return err
	}
	found = slices.DeleteFunc(found, notWorkspace)
	if len(found) == 0 {
		return provider.ErrNotFound
	}
	for _, r := range found {
		if _, err := p.docker.Run(ctx, nounContainer, verbStart, "--", r.UID); err != nil {
			return fmt.Errorf("start workspace of session %s: %w", sessionID, err)
		}
	}
	return nil
}
