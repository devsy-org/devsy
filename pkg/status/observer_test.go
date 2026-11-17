package status

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestObserverNestedOperations(t *testing.T) {
	observer := NewMemoryReporter()
	presentation := NewMemoryReporter()
	cause := errors.New("private failure")
	ctx := WithObserver(context.Background(), observer)
	err := Run(
		ctx,
		presentation,
		Operation{Phase: PhaseBuildingImage},
		func(ctx context.Context) error {
			outer := OperationID(ctx)
			if outer == "" || ParentOperationID(ctx) != "" {
				t.Fatal("invalid outer context")
			}
			return Run(
				ctx,
				presentation,
				Operation{Phase: PhaseStartingContainer},
				func(ctx context.Context) error {
					if OperationID(ctx) == outer || ParentOperationID(ctx) != outer {
						t.Fatal("invalid inner context")
					}
					return cause
				},
			)
		},
	)
	if !errors.Is(err, cause) {
		t.Fatal("lost error")
	}
	assertNestedObserverEvents(t, observer, presentation, cause)
}

func assertNestedObserverEvents(t *testing.T, observer, presentation *MemoryReporter, cause error) {
	t.Helper()
	events := observer.Events()
	if len(events) != 4 || len(presentation.Events()) != 4 {
		t.Fatalf("unexpected events: %v", events)
	}
	if !errors.Is(events[2].Cause, cause) || events[1].ParentOperationID != events[0].OperationID {
		t.Fatal("lost causal relationship")
	}
	encoded, err := json.Marshal(events[2])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "Cause") {
		t.Fatal("cause serialized")
	}
}

func TestObserverPreservesPanicValue(t *testing.T) {
	observer := NewMemoryReporter()
	value := &struct{ marker string }{"private"}
	func() {
		defer func() {
			if recover() != value {
				t.Fatal("panic value changed")
			}
		}()
		_ = Run(
			WithObserver(context.Background(), observer),
			Nop(),
			Operation{Phase: PhaseBuildingImage},
			func(context.Context) error { panic(value) },
		)
	}()
	events := observer.Events()
	if len(events) != 2 || events[1].State != StateFailed || events[1].Cause != nil {
		t.Fatal("unexpected panic event")
	}
}

func TestNoStatusKeepsObserver(t *testing.T) {
	presentation, err := NewReporter(ReporterOptions{Format: formatPlain, NoStatus: true})
	if err != nil {
		t.Fatal(err)
	}
	observer := NewMemoryReporter()
	cause := errors.New("failure")
	err = Run(
		WithObserver(context.Background(), observer),
		presentation,
		Operation{Phase: PhaseBuildingImage},
		func(context.Context) error { return cause },
	)
	if !errors.Is(err, cause) {
		t.Fatal("lost error")
	}
	events := observer.Events()
	if len(events) != 2 || !errors.Is(events[1].Cause, cause) {
		t.Fatalf("lost observer events: %+v", events)
	}
}
