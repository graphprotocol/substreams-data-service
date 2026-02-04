package sidecar

import (
	"context"
	"math/big"
	"time"

	"connectrpc.com/connect"
	"github.com/graphprotocol/substreams-data-service/horizon"
	consumerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/consumer/v1"
	"github.com/graphprotocol/substreams-data-service/sidecar"
	"go.uber.org/zap"
)

// Init initializes a new payment session with a provider.
// This is called by substreams before connecting to a provider.
// Returns the initial RAV to use for authentication.
func (s *Sidecar) Init(
	ctx context.Context,
	req *connect.Request[consumerv1.InitRequest],
) (*connect.Response[consumerv1.InitResponse], error) {
	s.logger.Info("Init called",
		zap.String("provider_endpoint", req.Msg.ProviderEndpoint),
	)

	// Extract escrow account details
	ea := req.Msg.EscrowAccount
	payer, receiver, dataService := ea.Payer.ToEth(), ea.Receiver.ToEth(), ea.DataService.ToEth()

	// Create a new session
	session := s.sessions.Create(payer, receiver, dataService)

	s.logger.Debug("created session",
		zap.String("session_id", session.ID),
		zap.Stringer("payer", payer),
		zap.Stringer("receiver", receiver),
		zap.Stringer("data_service", dataService),
	)

	// Check if we have an existing RAV to continue from
	var existingRAV *horizon.SignedRAV
	if req.Msg.ExistingRav != nil {
		existingRAV = sidecar.ProtoSignedRAVToHorizon(req.Msg.ExistingRav)
		session.SetRAV(existingRAV)
	}

	// Create initial RAV (can be zero-value for new sessions)
	var initialRAV *horizon.SignedRAV
	var err error

	if existingRAV != nil {
		// Use the existing RAV
		initialRAV = existingRAV
	} else {
		// Create a zero-value RAV for new sessions
		// This establishes the session parameters without committing to any value
		var collectionID horizon.CollectionID
		// Collection ID can be derived from session or left empty for now

		initialRAV, err = s.signRAV(
			collectionID,
			payer,
			dataService,
			receiver,
			uint64(time.Now().UnixNano()),
			big.NewInt(0), // Zero value
			nil,           // No metadata yet
		)
		if err != nil {
			s.logger.Error("failed to sign initial RAV", zap.Error(err))
			return nil, connect.NewError(connect.CodeInternal, err)
		}

		session.SetRAV(initialRAV)
	}

	// In a full implementation, we would call the provider's PaymentGateway.StartSession
	// to register this session. For now, we return the signed RAV for the client to use.

	response := &consumerv1.InitResponse{
		Session:    session.ToSessionInfo(),
		PaymentRav: sidecar.HorizonSignedRAVToProto(initialRAV),
	}

	s.logger.Info("Init completed",
		zap.String("session_id", session.ID),
	)

	return connect.NewResponse(response), nil
}
