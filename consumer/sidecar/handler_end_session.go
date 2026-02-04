package sidecar

import (
	"context"
	"math/big"
	"time"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	commonv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/common/v1"
	consumerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/consumer/v1"
	"github.com/graphprotocol/substreams-data-service/sidecar"
)

// EndSession ends the current session and reports final usage.
// Called by substreams when the stream ends.
func (s *Sidecar) EndSession(
	ctx context.Context,
	req *connect.Request[consumerv1.EndSessionRequest],
) (*connect.Response[consumerv1.EndSessionResponse], error) {
	sessionID := req.Msg.SessionId

	s.logger.Info("EndSession called",
		zap.String("session_id", sessionID),
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

	// Get current RAV
	currentRAV := session.GetRAV()

	// Calculate final value
	var finalValue *big.Int
	if currentRAV != nil && currentRAV.Message != nil {
		if finalUsage != nil {
			finalValue = new(big.Int).Add(currentRAV.Message.ValueAggregate, finalUsage.Cost.ToNative())
		} else {
			finalValue = currentRAV.Message.ValueAggregate
		}
	} else {
		if finalUsage != nil {
			finalValue = finalUsage.Cost.ToNative()
		} else {
			finalValue = big.NewInt(0)
		}
	}

	// Create final RAV
	var collectionID [32]byte
	if currentRAV != nil && currentRAV.Message != nil {
		collectionID = currentRAV.Message.CollectionID
	}

	finalRAV, err := s.signRAV(
		collectionID,
		session.Payer,
		session.DataService,
		session.Receiver,
		uint64(time.Now().UnixNano()),
		finalValue,
		nil,
	)
	if err != nil {
		s.logger.Error("failed to sign final RAV", zap.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	session.SetRAV(finalRAV)

	// End the session
	session.End(commonv1.EndReason_END_REASON_COMPLETE)

	// Get total usage
	totalUsage := session.GetUsage()

	response := &consumerv1.EndSessionResponse{
		FinalRav:   sidecar.HorizonSignedRAVToProto(finalRAV),
		TotalUsage: totalUsage,
	}

	s.logger.Info("EndSession completed",
		zap.String("session_id", sessionID),
		zap.Uint64("total_blocks", totalUsage.BlocksProcessed),
		zap.Uint64("total_bytes", totalUsage.BytesTransferred),
	)

	return connect.NewResponse(response), nil
}
