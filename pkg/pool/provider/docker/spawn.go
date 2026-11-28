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
	verbCreate       = "create"
	resourcePrefix   = "devsy-pool-"
	rollbackTimeout  = 30 * time.Second
	createTimeout    = 30 * time.Second
	pullTimeout      = 5 * time.Minute
	verbPull         = "pull"
	nounImage        = "image"
	verbImageInspect = "inspect"
	flagPullNever    = "--pull=never"
	settleDelay      = 250 * time.Millisecond
)

// ErrReclaimIncomplete reports that a rollback could not confirm the
// session's objects are gone. A later Reclaim finishes the job.
var ErrReclaimIncomplete = errors.New("session resources may remain")

// Spawn creates the session's internal network and a workspace container on
// it. The container is created, not started: it only holds the labels and the
// network until the enforcer exists. If any step fails or ctx ends after
// something was created, every object labelled for the session is removed
// before Spawn returns, using a context that ignores the cancel.
//
// A docker command killed mid-create can still finish in the daemon, out of
// sight and after any cleanup. So a create that has started runs to its end
// (at most createTimeout) even when ctx ends, and ctx is checked between
// steps. The rollback also repeats until two sweeps in a row find nothing, for
// the case where the timeout kills a create anyway.
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
	unlock, err := p.locks.lock(ctx, spec.Session)
	if err != nil {
		return err
	}
	defer unlock()
	if err := p.prepare(ctx, spec); err != nil {
		return err
	}
	if err := p.create(ctx, spec); err != nil {
		return errors.Join(err, p.rollback(ctx, spec))
	}
	return nil
}

// prepare refuses an existing session and makes sure the image is local.
func (p *Provider) prepare(ctx context.Context, spec provider.Spec) error {
	existing, err := p.find(ctx, kinds, scope{pool: spec.Pool, session: spec.Session})
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return provider.ErrExists
	}
	return p.ensureImage(ctx, spec.Image)
}

// ensureImage pulls the image when the daemon does not have it, so a cold pull
// does not run inside the create bound. A pull creates no pool object, so
// unlike a create it stops at once when ctx ends.
func (p *Provider) ensureImage(ctx context.Context, image string) error {
	if _, err := p.docker.Run(ctx, nounImage, verbImageInspect, "--", image); err == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, p.pullBound)
	defer cancel()
	if _, err := p.docker.Run(ctx, verbPull, "--quiet", "--", image); err != nil {
		return fmt.Errorf("pull image: %w", err)
	}
	return nil
}

func (p *Provider) create(ctx context.Context, spec provider.Spec) error {
	run, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.createBound)
	defer cancel()
	out, err := p.step(ctx, run, networkCreateArgs(spec))
	if err != nil {
		return mapCreateError(err)
	}
	networkID, err := parseID(out)
	if err != nil {
		return err
	}
	_, err = p.step(ctx, run, containerCreateArgs(spec, networkID))
	return err
}

// step runs a command on run, which ignores a cancel of ctx, and then reports
// the cancel.
func (p *Provider) step(ctx, run context.Context, args []string) ([]byte, error) {
	out, err := p.docker.Run(run, args...)
	if err != nil {
		return nil, err
	}
	return out, ctx.Err()
}

// rollback removes by label, not by the IDs create saw, because a command
// that was killed may have created its object anyway.
func (p *Provider) rollback(ctx context.Context, spec provider.Spec) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.bound)
	defer cancel()
	if err := p.sweepUntilQuiet(ctx, spec.Pool, spec.Session); err != nil {
		return fmt.Errorf(
			"rollback of session %s: %w",
			spec.Session,
			errors.Join(ErrReclaimIncomplete, err),
		)
	}
	return nil
}

func (p *Provider) sweepUntilQuiet(ctx context.Context, pool, session string) error {
	quiet := 0
	for quiet < 2 {
		n, err := p.reclaim(ctx, pool, session)
		if err != nil {
			return err
		}
		if n > 0 {
			quiet = 0
		} else {
			quiet++
		}
		if quiet < 2 {
			if err := sleep(ctx, p.settle); err != nil {
				return err
			}
		}
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
	args = append(args, "--label", provider.LabelRole+"="+provider.RoleWorkspace)
	args = append(args, "--network", networkID, flagPullNever)
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
