package sidecar

import (
	"context"

	"connectrpc.com/connect"
	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
)

func (s *Sidecar) GetSessionStatus(
	ctx context.Context,
	req *connect.Request[providerv1.GetSessionStatusRequest],
) (*connect.Response[providerv1.GetSessionStatusResponse], error) {
	panic("GetSessionStatus not implemented")
}
