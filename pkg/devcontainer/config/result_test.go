package config

import "testing"

func TestResultErr(t *testing.T) {
	tests := []struct {
		name    string
		result  *Result
		wantErr bool
		wantMsg string
	}{
		{name: "nil result", result: nil, wantErr: false},
		{name: "no error", result: &Result{}, wantErr: false},
		{name: "with error", result: &Result{Error: "boom"}, wantErr: true, wantMsg: "boom"},
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
