package sidecar

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
	"github.com/graphprotocol/substreams-data-service/sidecar"
)

// SubmitRAV submits a signed RAV to the provider sidecar.
// Called when the provider requests a new RAV for continued service.
func (s *Sidecar) SubmitRAV(
	ctx context.Context,
	req *connect.Request[providerv1.SubmitRAVRequest],
) (*connect.Response[providerv1.SubmitRAVResponse], error) {
	sessionID := req.Msg.SessionId

	s.logger.Info("SubmitRAV called",
		zap.String("session_id", sessionID),
	)

	// Get the session
	session, err := s.sessions.Get(sessionID)
	if err != nil {
		s.logger.Warn("session not found", zap.String("session_id", sessionID))
		return connect.NewResponse(&providerv1.SubmitRAVResponse{
			Accepted:        false,
			RejectionReason: "session not found",
			ShouldContinue:  false,
		}), nil
	}

	// Check session is active
	if !session.IsActive() {
		return connect.NewResponse(&providerv1.SubmitRAVResponse{
			Accepted:        false,
			RejectionReason: "session is not active",
			ShouldContinue:  false,
		}), nil
	}

	// Convert and validate the RAV
	signedRAV := sidecar.ProtoSignedRAVToHorizon(req.Msg.SignedRav)
	if signedRAV == nil || signedRAV.Message == nil {
		return connect.NewResponse(&providerv1.SubmitRAVResponse{
			Accepted:        false,
			RejectionReason: "invalid or missing RAV",
			ShouldContinue:  true,
		}), nil
	}

	// Verify signature
	signerAddr, err := s.verifyRAVSignature(signedRAV)
	if err != nil {
		s.logger.Warn("failed to verify RAV signature", zap.Error(err))
		return connect.NewResponse(&providerv1.SubmitRAVResponse{
			Accepted:        false,
			RejectionReason: fmt.Sprintf("signature verification failed: %v", err),
			ShouldContinue:  true,
		}), nil
	}

	// Check if signer is authorized
	if !s.isAcceptedSigner(signerAddr) {
		s.logger.Warn("RAV signer not authorized",
			zap.Stringer("signer", signerAddr),
		)
		return connect.NewResponse(&providerv1.SubmitRAVResponse{
			Accepted:        false,
			RejectionReason: fmt.Sprintf("signer %s is not authorized", signerAddr.Pretty()),
			ShouldContinue:  true,
		}), nil
	}

	// Verify RAV is for the correct session participants
	if !sidecar.AddressesEqual(signedRAV.Message.Payer, session.Payer) {
		return connect.NewResponse(&providerv1.SubmitRAVResponse{
			Accepted:        false,
			RejectionReason: "RAV payer does not match session",
			ShouldContinue:  true,
		}), nil
	}
	if !sidecar.AddressesEqual(signedRAV.Message.ServiceProvider, s.serviceProvider) {
		return connect.NewResponse(&providerv1.SubmitRAVResponse{
			Accepted:        false,
			RejectionReason: "RAV service provider does not match",
			ShouldContinue:  true,
		}), nil
	}

	// Verify RAV value is greater than or equal to previous RAV
	currentRAV := session.GetRAV()
	if currentRAV != nil && currentRAV.Message != nil {
		if signedRAV.Message.ValueAggregate.Cmp(currentRAV.Message.ValueAggregate) < 0 {
			return connect.NewResponse(&providerv1.SubmitRAVResponse{
				Accepted:        false,
				RejectionReason: "RAV value is less than current RAV",
				ShouldContinue:  true,
			}), nil
		}
	}

	// Store the new RAV
	session.SetRAV(signedRAV)

	s.logger.Info("SubmitRAV accepted",
		zap.String("session_id", sessionID),
		zap.Stringer("signer", signerAddr),
		zap.String("value", signedRAV.Message.ValueAggregate.String()),
	)

	response := &providerv1.SubmitRAVResponse{
		Accepted:       true,
		ShouldContinue: true,
	}

	return connect.NewResponse(response), nil
}
