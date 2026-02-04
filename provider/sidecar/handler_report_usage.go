package sidecar

import (
	"context"

	"connectrpc.com/connect"
	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
	"go.uber.org/zap"
)

// ReportUsage reports usage sent to a client.
// Called by the provider as data is sent during streaming.
func (s *Sidecar) ReportUsage(
	ctx context.Context,
	req *connect.Request[providerv1.ReportUsageRequest],
) (*connect.Response[providerv1.ReportUsageResponse], error) {
	sessionID := req.Msg.SessionId

	s.logger.Debug("ReportUsage called",
		zap.String("session_id", sessionID),
	)

	// Get the session
	session, err := s.sessions.Get(sessionID)
	if err != nil {
		s.logger.Warn("session not found", zap.String("session_id", sessionID))
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Check session is active
	if !session.IsActive() {
		return connect.NewResponse(&providerv1.ReportUsageResponse{
			ShouldContinue: false,
			StopReason:     "session is not active",
		}), nil
	}

	// Add usage to session
	usage := req.Msg.Usage
	if usage != nil {
		session.AddUsage(usage.BlocksProcessed, usage.BytesTransferred, usage.Requests, usage.Cost.ToNative())
	}

	// Check if we need to request a new RAV
	// In production, this would be based on thresholds (e.g., accumulated usage value)
	currentRAV := session.GetRAV()
	ravUpdated := currentRAV != nil

	response := &providerv1.ReportUsageResponse{
		ShouldContinue: true,
		RavUpdated:     ravUpdated,
	}

	s.logger.Debug("ReportUsage completed",
		zap.String("session_id", sessionID),
		zap.Uint64("total_blocks", session.BlocksProcessed),
		zap.Bool("rav_updated", ravUpdated),
	)

	return connect.NewResponse(response), nil
}
