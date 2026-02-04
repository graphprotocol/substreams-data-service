package sidecar

import (
	"context"

	"connectrpc.com/connect"
	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
)

func (s *Sidecar) EndSession(
	ctx context.Context,
	req *connect.Request[providerv1.EndSessionRequest],
) (*connect.Response[providerv1.EndSessionResponse], error) {
	panic("EndSession not implemented")
}
