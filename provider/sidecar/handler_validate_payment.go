package sidecar

import (
	"context"

	"connectrpc.com/connect"
	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
)

func (s *Sidecar) ValidatePayment(
	ctx context.Context,
	req *connect.Request[providerv1.ValidatePaymentRequest],
) (*connect.Response[providerv1.ValidatePaymentResponse], error) {
	panic("ValidatePayment not implemented")
}
