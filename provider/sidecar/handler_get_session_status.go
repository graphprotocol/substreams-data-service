package sidecar

import (
	"context"
	"math/big"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	commonv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/common/v1"
	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
)

// GetSessionStatus gets the current status of a payment session.
func (s *Sidecar) GetSessionStatus(
	ctx context.Context,
	req *connect.Request[providerv1.GetSessionStatusRequest],
) (*connect.Response[providerv1.GetSessionStatusResponse], error) {
	sessionID := req.Msg.SessionId

	s.logger.Debug("GetSessionStatus called",
		zap.String("session_id", sessionID),
	)

	// Get the session
	session, err := s.sessions.Get(sessionID)
	if err != nil {
		return connect.NewResponse(&providerv1.GetSessionStatusResponse{
			Active: false,
		}), nil
	}

	// Build session info
	sessionInfo := session.ToSessionInfo()

	// Build payment status
	currentRAV := session.GetRAV()
	var currentRavValue *big.Int
	if currentRAV != nil && currentRAV.Message != nil {
		currentRavValue = currentRAV.Message.ValueAggregate
	} else {
		currentRavValue = big.NewInt(0)
	}

	paymentStatus := &commonv1.PaymentStatus{
		CurrentRavValue:          commonv1.BigIntFromNative(currentRavValue),
		AccumulatedUsageValue:    commonv1.BigIntFromNative(session.TotalCost),
		EscrowBalance:            nil,  // TODO: Query from chain
		FundsSufficient:          true, // TODO: Calculate based on escrow balance
		EstimatedBlocksRemaining: 0,    // TODO: Calculate based on price and balance
	}

	response := &providerv1.GetSessionStatusResponse{
		Active:        session.IsActive(),
		Session:       sessionInfo,
		PaymentStatus: paymentStatus,
	}

	return connect.NewResponse(response), nil
}
