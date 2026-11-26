package docker

import (
	"context"
	"sync"
)

// sessionLocks serializes work on one session without holding up the others.
// A wait ends when its context does.
type sessionLocks struct {
	mu sync.Mutex
	m  map[string]*sessionLock
}

type sessionLock struct {
	held chan struct{}
	refs int
}

func (l *sessionLocks) lock(ctx context.Context, session string) (func(), error) {
	l.mu.Lock()
	if l.m == nil {
		l.m = map[string]*sessionLock{}
	}
	e := l.m[session]
	if e == nil {
		e = &sessionLock{held: make(chan struct{}, 1)}
		l.m[session] = e
	}
	e.refs++
	l.mu.Unlock()
	select {
	case e.held <- struct{}{}:
		return func() {
			<-e.held
			l.drop(session, e)
		}, nil
	case <-ctx.Done():
		l.drop(session, e)
		return nil, ctx.Err()
	}
}

func (l *sessionLocks) drop(session string, e *sessionLock) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e.refs--; e.refs == 0 {
		delete(l.m, session)
	}
}
