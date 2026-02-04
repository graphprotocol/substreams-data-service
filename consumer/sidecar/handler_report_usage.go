package sidecar

import (
	"context"
	"math/big"
	"time"

	"connectrpc.com/connect"
	consumerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/consumer/v1"
	"github.com/graphprotocol/substreams-data-service/sidecar"
	"go.uber.org/zap"
)

// ReportUsage reports usage received from the provider.
// Called by substreams as data is received during streaming.
// This may trigger RAV signing if the accumulated usage warrants it.
func (s *Sidecar) ReportUsage(
	ctx context.Context,
	req *connect.Request[consumerv1.ReportUsageRequest],
) (*connect.Response[consumerv1.ReportUsageResponse], error) {
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
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			connect.NewError(connect.CodeFailedPrecondition, nil))
	}

	// Add usage to session
	usage := req.Msg.Usage
	if usage != nil {
		session.AddUsage(usage.BlocksProcessed, usage.BytesTransferred, usage.Requests, usage.Cost.ToNative())
	}

	// Get current RAV for value calculation
	currentRAV := session.GetRAV()

	// Calculate new value aggregate
	var newValue *big.Int
	if currentRAV != nil && currentRAV.Message != nil {
		newValue = new(big.Int).Add(currentRAV.Message.ValueAggregate, usage.Cost.ToNative())
	} else {
		newValue = usage.Cost.ToNative()
	}

	// Create updated RAV with new value
	var collectionID [32]byte
	if currentRAV != nil && currentRAV.Message != nil {
		collectionID = currentRAV.Message.CollectionID
	}

	updatedRAV, err := s.signRAV(
		collectionID,
		session.Payer,
		session.DataService,
		session.Receiver,
		uint64(time.Now().UnixNano()),
		newValue,
		nil,
	)
	if err != nil {
		s.logger.Error("failed to sign updated RAV", zap.Error(err))
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	session.SetRAV(updatedRAV)

	response := &consumerv1.ReportUsageResponse{
		UpdatedRav:     sidecar.HorizonSignedRAVToProto(updatedRAV),
		ShouldContinue: true,
	}

	s.logger.Debug("ReportUsage completed",
		zap.String("session_id", sessionID),
		zap.Uint64("blocks_processed", session.BlocksProcessed),
	)

	return connect.NewResponse(response), nil
}
