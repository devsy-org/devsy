package engine

import (
	"github.com/devsy-org/devsy/pkg/pool/audit"
	"github.com/devsy-org/devsy/pkg/pool/session"
)

type nilStore struct{}

func (nilStore) Load(string) (session.Session, error) { return session.Session{}, nil }

func (nilStore) Transition(string, session.State, session.State, string) (session.Session, error) {
	return session.Session{}, nil
}

func (nilStore) CountReclaimAttempt(string) (session.Session, error) {
	return session.Session{}, nil
}

type nilAuditor struct{}

func (nilAuditor) Write(audit.Event) error { return nil }
