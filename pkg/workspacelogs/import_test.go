package workspacelogs

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// remoteRun builds a spool with one finished producer and returns its first
// page as the remote side would send it.
func remotePage(t *testing.T) (*ReadResponse, string) {
	t.Helper()
	root, runID := spoolWithRun(t, uidA)
	resp, err := ReadSpoolPage(SpoolSelector{
		Root: root, Context: testContext, WorkspaceUID: uidA, Read: ReadRequest{RunID: runID},
	})
	require.NoError(t, err)

	return resp, runID
}

// localRunFor creates the local run that mirrors the remote one.
func localRunFor(t *testing.T, runID, uid string) *Store {
	t.Helper()
	s, _ := newTestStore(t)
	_, err := s.CreateRun(
		RunSpec{RunID: runID, Context: testContext, WorkspaceUID: uid, Command: "up"},
	)
	require.NoError(t, err)

	return s
}

func importReq(runID, uid string, resp *ReadResponse) ImportRequest {
	return ImportRequest{RunID: runID, Context: testContext, WorkspaceUID: uid, Response: resp}
}

func TestImportRemoteMergesAndDeduplicates(t *testing.T) {
	resp, runID := remotePage(t)
	local := localRunFor(t, runID, uidA)

	res, err := local.ImportRemote(importReq(runID, uidA, resp))
	require.NoError(t, err)
	require.Equal(t, len(resp.Records), res.Written)
	require.Empty(t, res.Stalled)

	again, err := local.ImportRemote(importReq(runID, uidA, resp))
	require.NoError(t, err)
	require.Zero(t, again.Written)
	require.Equal(t, len(resp.Records), again.Duplicates)

	got := readAll(t, local, ReadRequest{RunID: runID})
	require.Equal(t, "hello\n", texts(got))
	m, err := local.Manifest(runID)
	require.NoError(t, err)
	require.True(t, m.Complete())
}

func TestImportRemoteRejectsMismatchedIdentity(t *testing.T) {
	resp, runID := remotePage(t)
	local := localRunFor(t, runID, uidA)

	cases := map[string]ImportRequest{
		"run":     importReq(NewID(), uidA, resp),
		"context": {RunID: runID, Context: otherContext, WorkspaceUID: uidA, Response: resp},
		"uid":     importReq(runID, "uid-b", resp),
		"nil":     importReq(runID, uidA, nil),
	}
	for name, req := range cases {
		_, err := local.ImportRemote(req)
		require.Error(t, err, name)
	}
	require.Empty(t, readAll(t, local, ReadRequest{RunID: runID}))
}

func TestImportRemoteRejectsForeignRecordAndOversizedPage(t *testing.T) {
	resp, runID := remotePage(t)
	local := localRunFor(t, runID, uidA)

	foreign := *resp
	foreign.Records = append([]Record(nil), resp.Records...)
	foreign.Records[0].RunID = NewID()
	_, err := local.ImportRemote(importReq(runID, uidA, &foreign))
	require.ErrorIs(t, err, ErrIdentityConflict)

	big := *resp
	big.Records = make([]Record, MaxReadLimit+1)
	_, err = local.ImportRemote(importReq(runID, uidA, &big))
	require.ErrorIs(t, err, ErrInvalidRecord)

	bad := *resp
	bad.Records = append([]Record(nil), resp.Records...)
	bad.Records[1].Stream = "bogus"
	_, err = local.ImportRemote(importReq(runID, uidA, &bad))
	require.ErrorIs(t, err, ErrInvalidRecord)
	require.Empty(t, readAll(t, local, ReadRequest{RunID: runID}))
}

func TestImportRemoteStallsAtRetentionGap(t *testing.T) {
	resp, runID := remotePage(t)
	local := localRunFor(t, runID, uidA)

	gapped := *resp
	gapped.Records = []Record{
		resp.Records[0],
		capture(Record{RunID: runID, ProducerID: resp.Records[0].ProducerID, Sequence: 2},
			ConditionRetentionGap, "sequences 2-3 are not retained"),
		resp.Records[3],
	}
	res, err := local.ImportRemote(importReq(runID, uidA, &gapped))
	require.NoError(t, err)
	require.Equal(t, 1, res.Written)
	require.Equal(t, []string{resp.Records[0].ProducerID}, res.Stalled)

	m, err := local.Manifest(runID)
	require.NoError(t, err)
	require.Contains(t, m.Conditions, ConditionRetentionGap)
	require.False(t, m.Complete())
}

func TestImportRemoteSkipAheadStalls(t *testing.T) {
	resp, runID := remotePage(t)
	local := localRunFor(t, runID, uidA)

	ahead := *resp
	ahead.Records = resp.Records[2:]
	res, err := local.ImportRemote(importReq(runID, uidA, &ahead))
	require.NoError(t, err)
	require.Zero(t, res.Written)
	require.Len(t, res.Stalled, 1)
}

func TestImportRemoteSealedDiscoveryRejectsUnknownProducer(t *testing.T) {
	resp, runID := remotePage(t)
	local := localRunFor(t, runID, uidA)
	require.NoError(t, local.SealDiscovery(runID))

	res, err := local.ImportRemote(importReq(runID, uidA, resp))
	require.NoError(t, err)
	require.Zero(t, res.Written)
	require.Len(t, res.Stalled, 1)
}

func TestLocalPositionsResumeAfterImport(t *testing.T) {
	resp, runID := remotePage(t)
	local := localRunFor(t, runID, uidA)
	_, err := local.ImportRemote(importReq(runID, uidA, resp))
	require.NoError(t, err)

	pos, err := local.LocalPositions(runID)
	require.NoError(t, err)
	producerID := resp.Records[0].ProducerID
	require.Equal(t, uint64(len(resp.Records)), pos[producerID])

	token, err := PositionCursor(runID, pos)
	require.NoError(t, err)
	next, err := local.Read(ReadRequest{RunID: runID, After: token})
	require.NoError(t, err)
	require.Empty(t, next.Records)
}

func TestImportRemoteDoesNotBindUnboundRunToResponseUID(t *testing.T) {
	resp, runID := remotePage(t)
	local := localRunFor(t, runID, "")

	_, err := local.ImportRemote(importReq(runID, uidA, resp))
	require.NoError(t, err)
	m, err := local.Manifest(runID)
	require.NoError(t, err)
	require.Nil(t, m.WorkspaceUID)
}
