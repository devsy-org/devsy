package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/session"
)

const (
	verbCreate      = "create"
	resourcePrefix  = "devsy-pool-"
	rollbackTimeout = 30 * time.Second
)

// Spawn creates the session's internal network and a workspace container on
// it. The container is created, not started: it only holds the labels and the
// network until the enforcer exists. If any step fails or ctx ends after
// something was created, every object labelled for the session is removed
// before Spawn returns, using a context that ignores the cancel.
func (p *Provider) Spawn(ctx context.Context, spec provider.Spec) error {
	if err := checkPool(ctx, spec.Pool); err != nil {
		return err
	}
	if !session.OwnsID(spec.Pool, spec.Session) {
		return provider.ErrInvalidSession
	}
	if err := provider.CheckImage(spec.Image); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	existing, err := p.find(ctx, kinds, scope{pool: spec.Pool, session: spec.Session})
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return provider.ErrExists
	}
	if err := p.create(ctx, spec); err != nil {
		return errors.Join(err, p.rollback(ctx, spec))
	}
	return nil
}

func (p *Provider) create(ctx context.Context, spec provider.Spec) error {
	out, err := p.docker.Run(ctx, networkCreateArgs(spec)...)
	if err != nil {
		return err
	}
	networkID, err := parseID(out)
	if err != nil {
		return err
	}
	_, err = p.docker.Run(ctx, containerCreateArgs(spec, networkID)...)
	return err
}

// rollback removes by label, not by the IDs create saw, because a command
// that was killed may have created its object anyway.
func (p *Provider) rollback(ctx context.Context, spec provider.Spec) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	if err := p.reclaim(ctx, spec.Pool, spec.Session); err != nil {
		return fmt.Errorf("rollback of session %s: %w", spec.Session, err)
	}
	return nil
}

func networkCreateArgs(spec provider.Spec) []string {
	args := []string{kindNetwork, verbCreate, "--internal"}
	args = append(args, labelArgs(spec)...)
	return append(args, "--", resourcePrefix+spec.Session)
}

func containerCreateArgs(spec provider.Spec, networkID string) []string {
	args := []string{
		nounContainer,
		verbCreate,
		"--name",
		resourcePrefix + spec.Session + "-workspace",
	}
	args = append(args, labelArgs(spec)...)
	args = append(args, "--network", networkID)
	return append(args, "--", spec.Image)
}

func labelArgs(spec provider.Spec) []string {
	return []string{
		"--label", provider.LabelPool + "=" + spec.Pool,
		"--label", provider.LabelSession + "=" + spec.Session,
	}
}

func parseID(out []byte) (string, error) {
	id := string(bytes.TrimSpace(out))
	if !validID(id) {
		return "", errors.New("docker create returned an unexpected ID")
	}
	return id, nil
}
