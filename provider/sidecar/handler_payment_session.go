package sidecar

import (
	"context"

	"connectrpc.com/connect"
	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
)

func (s *Sidecar) PaymentSession(
	ctx context.Context,
	stream *connect.BidiStream[providerv1.PaymentSessionRequest, providerv1.PaymentSessionResponse],
) error {
	panic("PaymentSession not implemented")
}
