package sidecar

import (
	"context"

	"connectrpc.com/connect"
	consumerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/consumer/v1"
)

func (s *Sidecar) Init(
	ctx context.Context,
	req *connect.Request[consumerv1.InitRequest],
) (*connect.Response[consumerv1.InitResponse], error) {
	panic("Init not implemented")
}
