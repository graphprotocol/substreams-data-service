package sidecar

import (
	"context"
	"io"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
	"github.com/graphprotocol/substreams-data-service/sidecar"
)

// PaymentSession is a bidirectional stream for ongoing payment negotiation.
// This allows the provider sidecar to request RAVs and notify about
// funding requirements in real-time.
func (s *Sidecar) PaymentSession(
	ctx context.Context,
	stream *connect.BidiStream[providerv1.PaymentSessionRequest, providerv1.PaymentSessionResponse],
) error {
	s.logger.Info("PaymentSession stream started")

	for {
		// Receive message from consumer sidecar
		msg, err := stream.Receive()
		if err == io.EOF {
			s.logger.Info("PaymentSession stream closed by client")
			return nil
		}
		if err != nil {
			s.logger.Error("PaymentSession receive error", zap.Error(err))
			return err
		}

		// Handle the message based on type
		switch m := msg.Message.(type) {
		case *providerv1.PaymentSessionRequest_RavSubmission:
			s.handleRAVSubmission(ctx, stream, m.RavSubmission)

		case *providerv1.PaymentSessionRequest_FundsAck:
			s.handleFundsAcknowledgment(ctx, stream, m.FundsAck)

		case *providerv1.PaymentSessionRequest_UsageReport:
			s.handleUsageReport(ctx, stream, m.UsageReport)

		default:
			s.logger.Warn("unknown message type in PaymentSession")
		}
	}
}

func (s *Sidecar) handleRAVSubmission(
	ctx context.Context,
	stream *connect.BidiStream[providerv1.PaymentSessionRequest, providerv1.PaymentSessionResponse],
	submission *providerv1.SignedRAVSubmission,
) {
	s.logger.Debug("received RAV submission in stream")

	// Validate the RAV
	signedRAV := sidecar.ProtoSignedRAVToHorizon(submission.SignedRav)
	if signedRAV == nil || signedRAV.Message == nil {
		s.logger.Warn("invalid RAV submission")
		// Send stop message
		stream.Send(&providerv1.PaymentSessionResponse{
			Message: &providerv1.PaymentSessionResponse_SessionControl{
				SessionControl: &providerv1.SessionControl{
					Action: providerv1.SessionControl_ACTION_STOP,
					Reason: "invalid RAV",
				},
			},
		})
		return
	}

	// Verify signature
	signerAddr, err := s.verifyRAVSignature(signedRAV)
	if err != nil {
		s.logger.Warn("RAV signature verification failed", zap.Error(err))
		stream.Send(&providerv1.PaymentSessionResponse{
			Message: &providerv1.PaymentSessionResponse_SessionControl{
				SessionControl: &providerv1.SessionControl{
					Action: providerv1.SessionControl_ACTION_STOP,
					Reason: "signature verification failed",
				},
			},
		})
		return
	}

	// Check if signer is authorized
	if !s.isAcceptedSigner(signerAddr) {
		s.logger.Warn("RAV signer not authorized", zap.Stringer("signer", signerAddr))
		stream.Send(&providerv1.PaymentSessionResponse{
			Message: &providerv1.PaymentSessionResponse_SessionControl{
				SessionControl: &providerv1.SessionControl{
					Action: providerv1.SessionControl_ACTION_STOP,
					Reason: "signer not authorized",
				},
			},
		})
		return
	}

	s.logger.Info("RAV accepted via stream",
		zap.Stringer("signer", signerAddr),
		zap.String("value", signedRAV.Message.ValueAggregate.String()),
	)

	// Send continue message
	stream.Send(&providerv1.PaymentSessionResponse{
		Message: &providerv1.PaymentSessionResponse_SessionControl{
			SessionControl: &providerv1.SessionControl{
				Action: providerv1.SessionControl_ACTION_CONTINUE,
			},
		},
	})
}

func (s *Sidecar) handleFundsAcknowledgment(
	ctx context.Context,
	stream *connect.BidiStream[providerv1.PaymentSessionRequest, providerv1.PaymentSessionResponse],
	ack *providerv1.FundsAcknowledgment,
) {
	s.logger.Debug("received funds acknowledgment",
		zap.Bool("will_deposit", ack.WillDeposit),
	)

	// For now, just continue if they will deposit
	if ack.WillDeposit {
		stream.Send(&providerv1.PaymentSessionResponse{
			Message: &providerv1.PaymentSessionResponse_SessionControl{
				SessionControl: &providerv1.SessionControl{
					Action: providerv1.SessionControl_ACTION_CONTINUE,
				},
			},
		})
	} else {
		// They won't deposit more funds, stop the session
		stream.Send(&providerv1.PaymentSessionResponse{
			Message: &providerv1.PaymentSessionResponse_SessionControl{
				SessionControl: &providerv1.SessionControl{
					Action: providerv1.SessionControl_ACTION_STOP,
					Reason: "insufficient funds and no deposit planned",
				},
			},
		})
	}
}

func (s *Sidecar) handleUsageReport(
	ctx context.Context,
	stream *connect.BidiStream[providerv1.PaymentSessionRequest, providerv1.PaymentSessionResponse],
	report *providerv1.UsageReport,
) {
	s.logger.Debug("received usage report via stream",
		zap.Uint64("blocks", report.Usage.GetBlocksProcessed()),
	)

	// Acknowledge the usage report
	stream.Send(&providerv1.PaymentSessionResponse{
		Message: &providerv1.PaymentSessionResponse_SessionControl{
			SessionControl: &providerv1.SessionControl{
				Action: providerv1.SessionControl_ACTION_CONTINUE,
			},
		},
	})
}
