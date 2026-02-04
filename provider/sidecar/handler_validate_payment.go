package sidecar

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	commonv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/common/v1"
	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
	"github.com/graphprotocol/substreams-data-service/sidecar"
)

// ValidatePayment validates a RAV received from a client.
// Called by the provider when a client connects with a payment header.
func (s *Sidecar) ValidatePayment(
	ctx context.Context,
	req *connect.Request[providerv1.ValidatePaymentRequest],
) (*connect.Response[providerv1.ValidatePaymentResponse], error) {
	s.logger.Info("ValidatePayment called")

	// Convert proto RAV to horizon RAV for verification
	signedRAV := sidecar.ProtoSignedRAVToHorizon(req.Msg.PaymentRav)
	if signedRAV == nil || signedRAV.Message == nil {
		return connect.NewResponse(&providerv1.ValidatePaymentResponse{
			Valid:           false,
			RejectionReason: "invalid or missing RAV",
		}), nil
	}

	// Verify the signature
	signerAddr, err := s.verifyRAVSignature(signedRAV)
	if err != nil {
		s.logger.Warn("failed to verify RAV signature", zap.Error(err))
		return connect.NewResponse(&providerv1.ValidatePaymentResponse{
			Valid:           false,
			RejectionReason: fmt.Sprintf("signature verification failed: %v", err),
		}), nil
	}

	// Check if signer is authorized
	if !s.isAcceptedSigner(signerAddr) {
		s.logger.Warn("signer not authorized",
			zap.Stringer("signer", signerAddr),
		)
		return connect.NewResponse(&providerv1.ValidatePaymentResponse{
			Valid:           false,
			RejectionReason: fmt.Sprintf("signer %s is not authorized", signerAddr.Pretty()),
		}), nil
	}

	// Verify RAV is for this service provider
	if !sidecar.AddressesEqual(signedRAV.Message.ServiceProvider, s.serviceProvider) {
		s.logger.Warn("RAV is for different service provider",
			zap.Stringer("expected", s.serviceProvider),
			zap.Stringer("got", signedRAV.Message.ServiceProvider),
		)
		return connect.NewResponse(&providerv1.ValidatePaymentResponse{
			Valid:           false,
			RejectionReason: "RAV is for a different service provider",
		}), nil
	}

	// Create or get session
	payer := signedRAV.Message.Payer
	dataService := signedRAV.Message.DataService

	// Look for existing session or create new one
	var session *sidecar.Session
	if req.Msg.ClientSessionId != "" {
		var err error
		session, err = s.sessions.Get(req.Msg.ClientSessionId)
		if err != nil {
			// Create new session if not found
			session = s.sessions.Create(payer, s.serviceProvider, dataService)
		}
	} else {
		session = s.sessions.Create(payer, s.serviceProvider, dataService)
	}

	// Store the RAV
	session.SetRAV(signedRAV)

	// Set pricing config on session
	session.SetPricingConfig(s.pricingConfig)

	// Query escrow balance from chain
	var availableBalance *commonv1.BigInt
	if escrowBalance, err := s.GetEscrowBalance(ctx, payer); err != nil {
		s.logger.Warn("failed to query escrow balance", zap.Error(err))
	} else if escrowBalance != nil {
		availableBalance = commonv1.BigIntFromNative(escrowBalance)
	}

	// Build response
	response := &providerv1.ValidatePaymentResponse{
		Valid:         true,
		SessionId:     session.ID,
		ServiceParams: req.Msg.ServiceParams, // Echo back the service params
		EscrowAccount: &commonv1.EscrowAccount{
			Payer:       commonv1.AddressFromEth(payer),
			Receiver:    commonv1.AddressFromEth(s.serviceProvider),
			DataService: commonv1.AddressFromEth(dataService),
		},
		AvailableBalance: availableBalance,
	}

	s.logger.Info("ValidatePayment succeeded",
		zap.String("session_id", session.ID),
		zap.Stringer("payer", payer),
		zap.Stringer("signer", signerAddr),
	)

	return connect.NewResponse(response), nil
}
