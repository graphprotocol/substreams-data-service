package sidecar

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
	"github.com/graphprotocol/substreams-data-service/sidecar"
)

// StartSession initiates a payment session with the provider.
// The consumer sidecar calls this to establish a session before
// the substreams client connects to the provider.
func (s *Sidecar) StartSession(
	ctx context.Context,
	req *connect.Request[providerv1.StartSessionRequest],
) (*connect.Response[providerv1.StartSessionResponse], error) {
	s.logger.Info("StartSession called")

	// Extract escrow account
	ea := req.Msg.EscrowAccount
	payer, receiver, dataService := ea.Payer.ToEth(), ea.Receiver.ToEth(), ea.DataService.ToEth()

	// Verify receiver matches this service provider
	if !sidecar.AddressesEqual(receiver, s.serviceProvider) {
		s.logger.Warn("escrow account receiver mismatch",
			zap.Stringer("expected", s.serviceProvider),
			zap.Stringer("got", receiver),
		)
		return connect.NewResponse(&providerv1.StartSessionResponse{
			Accepted:        false,
			RejectionReason: "escrow account receiver does not match this service provider",
		}), nil
	}

	// Validate initial RAV if provided
	initialRAV := sidecar.ProtoSignedRAVToHorizon(req.Msg.InitialRav)
	if initialRAV != nil && initialRAV.Message != nil {
		// Verify signature
		signerAddr, err := s.verifyRAVSignature(initialRAV)
		if err != nil {
			s.logger.Warn("failed to verify initial RAV signature", zap.Error(err))
			return connect.NewResponse(&providerv1.StartSessionResponse{
				Accepted:        false,
				RejectionReason: fmt.Sprintf("initial RAV signature verification failed: %v", err),
			}), nil
		}

		// Check if signer is authorized
		if !s.isAcceptedSigner(signerAddr) {
			s.logger.Warn("initial RAV signer not authorized",
				zap.Stringer("signer", signerAddr),
			)
			return connect.NewResponse(&providerv1.StartSessionResponse{
				Accepted:        false,
				RejectionReason: fmt.Sprintf("signer %s is not authorized", signerAddr.Pretty()),
			}), nil
		}

		// Verify RAV addresses match
		if !sidecar.AddressesEqual(initialRAV.Message.Payer, payer) {
			return connect.NewResponse(&providerv1.StartSessionResponse{
				Accepted:        false,
				RejectionReason: "RAV payer does not match escrow account payer",
			}), nil
		}
		if !sidecar.AddressesEqual(initialRAV.Message.ServiceProvider, s.serviceProvider) {
			return connect.NewResponse(&providerv1.StartSessionResponse{
				Accepted:        false,
				RejectionReason: "RAV service provider does not match",
			}), nil
		}
	}

	// Create session
	session := s.sessions.Create(payer, s.serviceProvider, dataService)
	if initialRAV != nil {
		session.SetRAV(initialRAV)
	}

	s.logger.Info("StartSession succeeded",
		zap.String("session_id", session.ID),
		zap.Stringer("payer", payer),
	)

	// Return the RAV to use (same as initial for now)
	response := &providerv1.StartSessionResponse{
		SessionId: session.ID,
		UseRav:    req.Msg.InitialRav, // Use the same RAV
		Accepted:  true,
	}

	return connect.NewResponse(response), nil
}
