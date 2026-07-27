package config

import (
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/clierr"
)

const errBoom = "boom"

func TestResultErrRecoveryAvailable(t *testing.T) {
	err := (&Result{Error: "build image: boom", RecoveryAvailable: true}).Err()
	if err == nil || err.Error() != "build image: boom" {
		t.Fatalf("Err() = %v, want message preserved", err)
	}
	if !errors.Is(err, clierr.ErrBuildFailedRecoverable) {
		t.Fatal("recovery-available error must classify as recoverable")
	}

	plain := (&Result{Error: errBoom}).Err()
	if errors.Is(plain, clierr.ErrBuildFailedRecoverable) {
		t.Fatal("plain error must not be recoverable")
	}
}

func TestResultErr(t *testing.T) {
	tests := []struct {
		name    string
		result  *Result
		wantErr bool
		wantMsg string
	}{
		{name: "nil result", result: nil, wantErr: false},
		{name: "no error", result: &Result{}, wantErr: false},
		{name: "with error", result: &Result{Error: errBoom}, wantErr: true, wantMsg: errBoom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.result.Err()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if err.Error() != tt.wantMsg {
					t.Fatalf("expected %q, got %q", tt.wantMsg, err.Error())
				}
			} else if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}
