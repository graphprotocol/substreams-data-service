package sidecar

import (
	"context"

	"connectrpc.com/connect"
	commonv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/common/v1"
	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
	"github.com/graphprotocol/substreams-data-service/sidecar"
	"go.uber.org/zap"
)

// EndSession ends a session and reports final usage.
// Called by the provider when a stream ends.
func (s *Sidecar) EndSession(
	ctx context.Context,
	req *connect.Request[providerv1.EndSessionRequest],
) (*connect.Response[providerv1.EndSessionResponse], error) {
	sessionID := req.Msg.SessionId

	s.logger.Info("EndSession called",
		zap.String("session_id", sessionID),
		zap.Stringer("reason", req.Msg.Reason),
	)

	// Get the session
	session, err := s.sessions.Get(sessionID)
	if err != nil {
		s.logger.Warn("session not found", zap.String("session_id", sessionID))
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Add final usage if provided
	finalUsage := req.Msg.FinalUsage
	if finalUsage != nil {
		session.AddUsage(finalUsage.BlocksProcessed, finalUsage.BytesTransferred, finalUsage.Requests, finalUsage.Cost.ToNative())
	}

	// End the session
	session.End(req.Msg.Reason)

	// Get the final RAV and usage
	finalRAV := session.GetRAV()
	totalUsage := session.GetUsage()

	response := &providerv1.EndSessionResponse{
		FinalRav:   sidecar.HorizonSignedRAVToProto(finalRAV),
		TotalUsage: totalUsage,
		TotalValue: commonv1.BigIntFromNative(session.TotalCost),
	}

	s.logger.Info("EndSession completed",
		zap.String("session_id", sessionID),
		zap.Uint64("total_blocks", totalUsage.BlocksProcessed),
		zap.Uint64("total_bytes", totalUsage.BytesTransferred),
	)

	return connect.NewResponse(response), nil
}
