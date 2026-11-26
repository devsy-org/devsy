package tunnelserver

import (
	"context"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// AppendSetupLogs validates a batch against the expected run and persists it.
// Without a sink the RPC is unimplemented, like an older peer.
func (t *tunnelServer) AppendSetupLogs(
	_ context.Context, req *tunnel.AppendSetupLogsRequest,
) (*tunnel.AppendSetupLogsResponse, error) {
	if t.setupLogSink == nil {
		return nil, status.Error(codes.Unimplemented, "setup log mirroring is not enabled")
	}
	if proto.Size(req) > workspacelogs.MaxPageBytes {
		return rejectedBatch("batch exceeds page size"), nil
	}
	res, err := t.setupLogSink.Append(workspacelogs.BatchRequest{
		SchemaVersion: int(req.GetSchemaVersion()),
		RunID:         req.GetRunId(),
		ProducerID:    req.GetProducerId(),
		FirstSequence: req.GetFirstSequence(),
		LastSequence:  req.GetLastSequence(),
		Records:       req.GetRecords(),
	})
	if err != nil {
		return nil, status.Error(codes.Unavailable, "setup log store failed")
	}

	return &tunnel.AppendSetupLogsResponse{
		AcceptedSequence: res.Accepted,
		Result:           protoResult(res.Result),
		GapFirstSequence: res.GapFirst,
		GapLastSequence:  res.GapLast,
		Detail:           res.Detail,
	}, nil
}

func rejectedBatch(detail string) *tunnel.AppendSetupLogsResponse {
	return &tunnel.AppendSetupLogsResponse{
		Result: tunnel.SetupLogResult_SETUP_LOG_RESULT_REJECTED_INVALID,
		Detail: detail,
	}
}

func protoResult(r workspacelogs.MirrorResult) tunnel.SetupLogResult {
	switch r {
	case workspacelogs.MirrorAccepted:
		return tunnel.SetupLogResult_SETUP_LOG_RESULT_ACCEPTED
	case workspacelogs.MirrorRetentionGap:
		return tunnel.SetupLogResult_SETUP_LOG_RESULT_RETENTION_GAP
	default:
		return tunnel.SetupLogResult_SETUP_LOG_RESULT_REJECTED_INVALID
	}
}
