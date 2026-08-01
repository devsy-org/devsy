package snapshot

import (
	"encoding/json"
	"testing"
	"time"

	snapshotpkg "github.com/devsy-org/devsy/pkg/snapshot"
	"github.com/stretchr/testify/require"
)

const testRepository = "registry/repo"

func TestPrintJSONRefs(t *testing.T) {
	ts := time.Date(2026, 7, 31, 23, 45, 0, 0, time.UTC)
	refs := []*snapshotpkg.Ref{
		{Repository: testRepository, WorkspaceID: "ws1", Timestamp: ts, Tag: "ws1-20260731234500"},
		{
			Repository:  testRepository,
			WorkspaceID: "ws2",
			Timestamp:   ts.Add(1 * time.Second),
			Tag:         "ws2-20260731234501",
		},
	}

	err := printJSONRefs(refs)
	require.NoError(t, err)
}

func TestMarshalRefs(t *testing.T) {
	ts := time.Date(2026, 7, 31, 23, 45, 0, 0, time.UTC)
	refs := []*snapshotpkg.Ref{
		{Repository: testRepository, WorkspaceID: "ws1", Timestamp: ts, Tag: "ws1-20260731234500"},
	}

	data, err := json.Marshal(refs)
	require.NoError(t, err)

	var unmarshaled []*snapshotpkg.Ref
	err = json.Unmarshal(data, &unmarshaled)
	require.NoError(t, err)
	require.Len(t, unmarshaled, 1)
	require.Equal(t, "ws1", unmarshaled[0].WorkspaceID)
	require.Equal(t, "ws1-20260731234500", unmarshaled[0].Tag)
}
