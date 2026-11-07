package dependencytest

import (
	"errors"
	"fmt"
	"slices"
)

type LockKind string

const (
	ArtifactLock LockKind = "artifact"
	GateLock     LockKind = "G"
	ConfigLock   LockKind = "config"
	InitLock     LockKind = "init"
)

type WorkKind string

const (
	MetadataWork  WorkKind = "bounded metadata"
	ProcessWork   WorkKind = "process"
	NetworkWork   WorkKind = "network"
	PromptWork    WorkKind = "prompt"
	RuntimeWork   WorkKind = "runtime"
	WorkspaceWork WorkKind = "workspace"
)

// LockOrder checks a single test trace's reported events without taking locks.
type LockOrder struct {
	held []LockKind
}

func (s *LockOrder) Acquire(lock LockKind) error {
	if slices.Contains(s.held, lock) {
		return fmt.Errorf("lock %s already held", lock)
	}
	switch lock {
	case ArtifactLock:
	case GateLock:
		if err := s.canAcquireGate(); err != nil {
			return err
		}
	case ConfigLock:
		if !slices.Contains(s.held, GateLock) {
			return errors.New("config requires G")
		}
	default:
		return errors.New(
			"only artifact, G and config may use blocking acquisition; init requires TryInit",
		)
	}
	s.held = append(s.held, lock)
	return nil
}

func (s *LockOrder) TryInit(acquired bool) error {
	if slices.Contains(s.held, InitLock) {
		return errors.New("init already held")
	}
	if acquired {
		s.held = append(s.held, InitLock)
	}
	return nil
}

func (s *LockOrder) Release(lock LockKind) error {
	if len(s.held) == 0 || s.held[len(s.held)-1] != lock {
		return fmt.Errorf("release %s in reverse acquisition order", lock)
	}
	s.held = s.held[:len(s.held)-1]
	return nil
}

func (s *LockOrder) Work(work WorkKind) error {
	switch work {
	case MetadataWork:
		if !slices.Contains(s.held, ConfigLock) {
			return errors.New("bounded metadata requires G then config")
		}
	case ProcessWork, NetworkWork, PromptWork, RuntimeWork, WorkspaceWork:
		if slices.Contains(s.held, GateLock) || slices.Contains(s.held, ConfigLock) {
			return fmt.Errorf("%s forbidden under G/config", work)
		}
	default:
		return fmt.Errorf("unknown work %q", work)
	}
	return nil
}

func (s *LockOrder) Check() error {
	if len(s.held) != 0 {
		return fmt.Errorf("locks still held: %v", s.held)
	}
	return nil
}

func (s *LockOrder) canAcquireGate() error {
	if slices.Contains(s.held, ArtifactLock) || slices.Contains(s.held, InitLock) {
		return errors.New("release artifact and init locks before acquiring G")
	}
	return nil
}
