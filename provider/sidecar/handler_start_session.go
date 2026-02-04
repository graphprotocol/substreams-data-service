package sidecar

import (
	"context"

	"connectrpc.com/connect"
	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
)

func (s *Sidecar) StartSession(
	ctx context.Context,
	req *connect.Request[providerv1.StartSessionRequest],
) (*connect.Response[providerv1.StartSessionResponse], error) {
	panic("StartSession not implemented")
}
