package sidecar

import (
	"context"

	"connectrpc.com/connect"
	consumerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/consumer/v1"
)

func (s *Sidecar) ReportUsage(
	ctx context.Context,
	req *connect.Request[consumerv1.ReportUsageRequest],
) (*connect.Response[consumerv1.ReportUsageResponse], error) {
	panic("ReportUsage not implemented")
}
