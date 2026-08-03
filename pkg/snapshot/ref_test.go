package snapshot

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewRef_FormatsWorkspaceAndTimestamp(t *testing.T) {
	at := time.Date(2026, 7, 31, 15, 4, 5, 0, time.UTC)
	ref, err := NewRef("ghcr.io/acme/snapshots", "my-ws", at)
	require.NoError(t, err)
	require.Regexp(t, `^ghcr\.io/acme/snapshots:my-ws-20260731150405-[a-z]{6}$`, ref.String())
}

func TestNewRef_AvoidsCollisionsWithinSameSecond(t *testing.T) {
	at := time.Date(2026, 7, 31, 15, 4, 5, 0, time.UTC)
	first, err := NewRef("ghcr.io/acme/snapshots", "my-ws", at)
	require.NoError(t, err)
	second, err := NewRef("ghcr.io/acme/snapshots", "my-ws", at)
	require.NoError(t, err)
	require.NotEqual(t, first.Tag, second.Tag)
}

func TestParseRef_RoundTrip(t *testing.T) {
	ref, err := ParseRef("ghcr.io/acme/snapshots:my-ws-20260731150405-abcxyz")
	require.NoError(t, err)
	require.Equal(t, "ghcr.io/acme/snapshots", ref.Repository)
	require.Equal(t, "my-ws", ref.WorkspaceID)
	require.Equal(t, "my-ws-20260731150405-abcxyz", ref.Tag)
}

func TestParseRef_RejectsMissingTag(t *testing.T) {
	_, err := ParseRef("ghcr.io/acme/snapshots")
	require.Error(t, err)
}

func TestNewRef_RejectsEmptyRepository(t *testing.T) {
	at := time.Date(2026, 7, 31, 15, 4, 5, 0, time.UTC)
	_, err := NewRef("", "my-ws", at)
	require.Error(t, err)
}

func TestNewRef_RejectsEmptyWorkspaceID(t *testing.T) {
	at := time.Date(2026, 7, 31, 15, 4, 5, 0, time.UTC)
	_, err := NewRef("ghcr.io/acme/snapshots", "", at)
	require.Error(t, err)
}

func TestParseRef_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		checks  func(t *testing.T, ref *Ref)
	}{
		{
			name:    "digest form ref should error",
			input:   "ghcr.io/acme/snapshots@sha256:abcdef1234567890",
			wantErr: true,
		},
		{
			name:    "port-carrying registry should parse",
			input:   "localhost:5000/snapshots:ws1-20260731120000-abcxyz",
			wantErr: false,
			checks: func(t *testing.T, ref *Ref) {
				require.Equal(t, "localhost:5000/snapshots", ref.Repository)
				require.Equal(t, "ws1", ref.WorkspaceID)
				require.Equal(t, "ws1-20260731120000-abcxyz", ref.Tag)
				require.Equal(t, "localhost:5000/snapshots:ws1-20260731120000-abcxyz", ref.String())
			},
		},
		{
			name:    "tag without timestamp separator should error",
			input:   "ghcr.io/acme/snapshots:notimestamp",
			wantErr: true,
		},
		{
			name:    "tag missing random suffix should error",
			input:   "ghcr.io/acme/snapshots:ws1-20260731120000",
			wantErr: true,
		},
		{
			name:    "tag with multiple hyphens splits on last two hyphens",
			input:   "ghcr.io/acme/snapshots:my-multi-ws-20260731120000-abcxyz",
			wantErr: false,
			checks: func(t *testing.T, ref *Ref) {
				require.Equal(t, "my-multi-ws", ref.WorkspaceID)
				require.Equal(t, "my-multi-ws-20260731120000-abcxyz", ref.Tag)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := ParseRef(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.checks != nil {
				tt.checks(t, ref)
			}
		})
	}
}
