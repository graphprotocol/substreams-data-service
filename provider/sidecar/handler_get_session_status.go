package sidecar

import (
	"context"
	"math/big"

	"connectrpc.com/connect"
	commonv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/common/v1"
	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
	"go.uber.org/zap"
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

	// Query escrow balance from chain
	var escrowBalance *big.Int
	if balance, err := s.GetEscrowBalance(ctx, session.Payer); err != nil {
		s.logger.Warn("failed to query escrow balance", zap.Error(err))
		escrowBalance = big.NewInt(0)
	} else if balance != nil {
		escrowBalance = balance
	} else {
		escrowBalance = big.NewInt(0)
	}

	// Calculate funds sufficiency: escrow balance > accumulated usage - current RAV
	// (RAV represents already committed payment, so we only need funds for uncommitted usage)
	uncommittedUsage := new(big.Int).Sub(session.TotalCost, currentRavValue)
	if uncommittedUsage.Sign() < 0 {
		uncommittedUsage = big.NewInt(0)
	}
	fundsSufficient := escrowBalance.Cmp(uncommittedUsage) >= 0

	// Calculate estimated blocks remaining based on price and available balance
	var estimatedBlocksRemaining uint64
	if session.PricePerBlock != nil && session.PricePerBlock.Sign() > 0 {
		availableFunds := new(big.Int).Sub(escrowBalance, uncommittedUsage)
		if availableFunds.Sign() > 0 {
			estimatedBlocksRemaining = new(big.Int).Div(availableFunds, session.PricePerBlock).Uint64()
		}
	}

	paymentStatus := &commonv1.PaymentStatus{
		CurrentRavValue:          commonv1.BigIntFromNative(currentRavValue),
		AccumulatedUsageValue:    commonv1.BigIntFromNative(session.TotalCost),
		EscrowBalance:            commonv1.BigIntFromNative(escrowBalance),
		FundsSufficient:          fundsSufficient,
		EstimatedBlocksRemaining: estimatedBlocksRemaining,
	}

	response := &providerv1.GetSessionStatusResponse{
		Active:        session.IsActive(),
		Session:       sessionInfo,
		PaymentStatus: paymentStatus,
	}

	return connect.NewResponse(response), nil
}
