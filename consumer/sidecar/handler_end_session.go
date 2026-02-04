package sidecar

import (
	"context"

	"connectrpc.com/connect"
	consumerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/consumer/v1"
)

func (s *Sidecar) EndSession(
	ctx context.Context,
	req *connect.Request[consumerv1.EndSessionRequest],
) (*connect.Response[consumerv1.EndSessionResponse], error) {
	panic("EndSession not implemented")
}
