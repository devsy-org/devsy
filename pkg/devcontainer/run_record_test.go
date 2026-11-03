package devcontainer

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const recordTestContext = "default"

func TestInitializeSkipIsRecordedWithItsReason(t *testing.T) {
	store, err := workspacelogs.Open(workspacelogs.Options{Root: t.TempDir()})
	require.NoError(t, err)
	session := workspacelogs.StartSessionIn(store, workspacelogs.SessionOptions{
		Run:      workspacelogs.RunSpec{Context: recordTestContext, Command: "up"},
		Location: workspacelogs.LocationHost,
	})
	require.NotNil(t, session)

	recordInitializeSkipped(session.Context(context.Background()), "recovery_excluded")
	recordInitializeSkipped(context.Background(), "ignored without a recorder")
	session.Finish(nil, true)

	resp, err := store.Read(
		workspacelogs.ReadRequest{RunID: session.RunID, Hook: "initializeCommand"},
	)
	require.NoError(t, err)
	var skipped []workspacelogs.Record
	for _, rec := range resp.Records {
		if rec.Kind == workspacelogs.KindInvocation {
			skipped = append(skipped, rec)
		}
	}
	require.Len(t, skipped, 1)
	assert.Equal(t, workspacelogs.InvocationSkipped, skipped[0].State)
	assert.Equal(t, "recovery_excluded", skipped[0].Reason)
}
