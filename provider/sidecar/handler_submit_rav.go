package sidecar

import (
	"context"

	"connectrpc.com/connect"
	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
)

func (s *Sidecar) SubmitRAV(
	ctx context.Context,
	req *connect.Request[providerv1.SubmitRAVRequest],
) (*connect.Response[providerv1.SubmitRAVResponse], error) {
	panic("SubmitRAV not implemented")
}
