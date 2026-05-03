package config

import (
	"testing"
)

const (
	testWorkspaceID = "workspace-123"
	testLabelApp    = "app=myapp"
)

func TestGetIDLabels(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		idLabels []string
		want     []string
	}{
		{
			name:     "no custom labels uses default",
			id:       testWorkspaceID,
			idLabels: nil,
			want:     []string{"dev.containers.id=" + testWorkspaceID},
		},
		{
			name:     "empty slice uses default",
			id:       testWorkspaceID,
			idLabels: []string{},
			want:     []string{"dev.containers.id=" + testWorkspaceID},
		},
		{
			name:     "custom labels replace default",
			id:       testWorkspaceID,
			idLabels: []string{"myapp.id=custom"},
			want:     []string{"myapp.id=custom"},
		},
		{
			name:     "multiple custom labels",
			id:       testWorkspaceID,
			idLabels: []string{testLabelApp, "env=dev"},
			want:     []string{testLabelApp, "env=dev"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetIDLabels(tt.id, tt.idLabels)
			if len(got) != len(tt.want) {
				t.Fatalf("GetIDLabels() returned %d labels, want %d", len(got), len(tt.want))
			}
			for i, label := range got {
				if label != tt.want[i] {
					t.Errorf("GetIDLabels()[%d] = %q, want %q", i, label, tt.want[i])
				}
			}
		})
	}
}

func TestValidateIDLabels(t *testing.T) {
	tests := []struct {
		name    string
		labels  []string
		wantErr bool
	}{
		{
			name:    "valid single label",
			labels:  []string{"key=value"},
			wantErr: false,
		},
		{
			name:    "valid multiple labels",
			labels:  []string{testLabelApp, "env=prod"},
			wantErr: false,
		},
		{
			name:    "empty value is valid",
			labels:  []string{"key="},
			wantErr: false,
		},
		{
			name:    "empty slice is valid",
			labels:  []string{},
			wantErr: false,
		},
		{
			name:    "missing equals sign",
			labels:  []string{"noequalssign"},
			wantErr: true,
		},
		{
			name:    "empty key",
			labels:  []string{"=value"},
			wantErr: true,
		},
		{
			name:    "one valid one invalid",
			labels:  []string{"good=label", "bad"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIDLabels(tt.labels)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateIDLabels() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
