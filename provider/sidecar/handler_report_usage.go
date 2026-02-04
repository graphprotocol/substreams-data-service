package sidecar

import (
	"context"

	"connectrpc.com/connect"
	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
)

func (s *Sidecar) ReportUsage(
	ctx context.Context,
	req *connect.Request[providerv1.ReportUsageRequest],
) (*connect.Response[providerv1.ReportUsageResponse], error) {
	panic("ReportUsage not implemented")
}
