package engine

import (
	"context"
	"errors"
	"slices"

	"github.com/devsy-org/devsy-signals/attrkey"
	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/session"
)

// Report counts what Reconcile found. Orphans counts runtime resources that no
// live session record owns.
type Report struct {
	Sessions int
	Failed   int
	Orphans  int
}

type restartPlan struct {
	failFirst       []string
	reclaim         []string
	orphanSessions  []string
	orphanResources []provider.Orphan
	report          Report
}

// Reconcile runs once when the pool starts, before it accepts work. It writes
// pool.start, then ends what the restart cut off. Every spawning, busy and
// idle session goes to failed with reason pool_restart and then to reclaiming.
// A ready session goes to reclaiming, because no egress probe exists yet to
// keep it warm. Queued sessions stay queued; reclaiming and orphaned sessions
// are left to Sweep. A resource whose session ID has no live record, or whose
// session label is missing or invalid, is an orphan. The first is reclaimed by
// its exact session ID with a session.reclaim event. The second is deleted
// one object at a time by UID and is counted in pool.start only, since it has
// no session to name. It stops at the first audit or store error and returns
// the report so far.
func (e *Engine) Reconcile(ctx context.Context) (Report, error) {
	sessions, err := e.store.List()
	if err != nil {
		return Report{}, err
	}
	plan, err := e.planRestart(ctx, sessions)
	if err != nil {
		return Report{}, err
	}
	if err := e.audit(envelope.AuditPoolStart, "", poolStartData(plan.report)); err != nil {
		return Report{}, err
	}
	if err := e.endForRestart(ctx, plan); err != nil {
		return plan.report, err
	}
	return plan.report, e.reclaimOrphans(ctx, plan)
}

func (e *Engine) planRestart(ctx context.Context, sessions []session.Session) (restartPlan, error) {
	listed, err := e.provider.List(ctx, e.pool)
	if err != nil {
		return restartPlan{}, err
	}
	owned, orphans, err := provider.Classify(e.pool, listed)
	if err != nil {
		return restartPlan{}, err
	}
	plan := restartPlan{orphanResources: orphans}
	plan.report.Sessions = len(sessions)
	live := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		if s.Pool != e.pool {
			return restartPlan{}, ErrForeignPool
		}
		live[s.ID] = s.State != session.StateDeleted
		plan.add(s)
	}
	for id, resources := range owned {
		if !live[id] {
			plan.orphanSessions = append(plan.orphanSessions, id)
			plan.report.Orphans += len(resources)
		}
	}
	slices.Sort(plan.orphanSessions)
	plan.report.Orphans += len(orphans)
	return plan, nil
}

func (p *restartPlan) add(s session.Session) {
	switch s.State {
	case session.StateSpawning, session.StateBusy, session.StateIdle:
		p.failFirst = append(p.failFirst, s.ID)
		p.report.Failed++
	case session.StateReady, session.StateFailed:
		p.reclaim = append(p.reclaim, s.ID)
	}
}

func poolStartData(r Report) map[string]any {
	return map[string]any{
		attrkey.PoolSessionsLoaded: r.Sessions,
		attrkey.PoolSessionsFailed: r.Failed,
		attrkey.PoolOrphansFound:   r.Orphans,
	}
}

// endForRestart skips a session that changed state since the listing. It
// checks the context before each session, so a canceled start stops after the
// transition in flight.
func (e *Engine) endForRestart(ctx context.Context, plan restartPlan) error {
	for _, id := range plan.failFirst {
		step := func() error { _, err := e.Fail(id, EndPoolRestart); return err }
		if err := e.restartStep(ctx, step); err != nil {
			return err
		}
	}
	for _, id := range slices.Concat(plan.failFirst, plan.reclaim) {
		step := func() error { _, err := e.BeginReclaim(id, EndPoolRestart); return err }
		if err := e.restartStep(ctx, step); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) restartStep(ctx context.Context, step func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := step(); err != nil && !errors.Is(err, ErrNotEndable) {
		return err
	}
	return nil
}

func (e *Engine) reclaimOrphans(ctx context.Context, plan restartPlan) error {
	var incomplete bool
	for _, id := range plan.orphanSessions {
		done, err := e.reclaimOrphanSession(ctx, id)
		if err != nil {
			return err
		}
		incomplete = incomplete || !done
	}
	failed, err := e.deleteStrays(ctx, plan.orphanResources)
	if err != nil {
		return err
	}
	if incomplete || failed {
		return ErrReclaimIncomplete
	}
	return nil
}

// deleteStrays deletes each listed object by UID. The provider ignores an
// object whose session label changed since the listing.
func (e *Engine) deleteStrays(ctx context.Context, strays []provider.Orphan) (bool, error) {
	var failed bool
	for _, o := range strays {
		if err := e.provider.Delete(ctx, e.pool, o.Resource); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return failed, ctxErr
			}
			failed = true
		}
	}
	return failed, ctx.Err()
}

// reclaimOrphanSession reclaims by exact session ID. A session without a
// record has no attempt count to store, so the event always says attempt one
// and a later Reconcile retries what is left.
func (e *Engine) reclaimOrphanSession(ctx context.Context, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	out := e.removeResources(ctx, id)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	data := reclaimData(session.Session{Attempts: 1}, out, true)
	if err := e.audit(envelope.AuditSessionReclaim, id, data); err != nil {
		return false, err
	}
	return out.result == resultDeleted, nil
}
