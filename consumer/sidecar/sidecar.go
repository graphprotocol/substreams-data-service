package sidecar

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/consumer/v1/consumerv1connect"
	"github.com/streamingfast/dgrpc/server"
	"github.com/streamingfast/dgrpc/server/connectrpc"
	"github.com/streamingfast/shutter"
	"go.uber.org/zap"
)

var _ consumerv1connect.ConsumerSidecarServiceHandler = (*Sidecar)(nil)

type Sidecar struct {
	*shutter.Shutter

	listenAddr string
	logger     *zap.Logger
	server     *connectrpc.ConnectWebServer
}

func New(listenAddr string, logger *zap.Logger) *Sidecar {
	return &Sidecar{
		Shutter:    shutter.New(),
		listenAddr: listenAddr,
		logger:     logger,
	}
}

func (s *Sidecar) Run() {
	handlerGetters := []connectrpc.HandlerGetter{
		func(opts ...connect.HandlerOption) (string, http.Handler) {
			return consumerv1connect.NewConsumerSidecarServiceHandler(s, opts...)
		},
	}

	s.server = connectrpc.New(
		handlerGetters,
		server.WithPlainTextServer(),
		server.WithLogger(s.logger),
		server.WithHealthCheck(server.HealthCheckOverHTTP, s.healthCheck),
		server.WithConnectPermissiveCORS(),
		server.WithConnectReflection(consumerv1connect.ConsumerSidecarServiceName),
	)

	s.server.OnTerminated(func(err error) {
		s.Shutdown(err)
	})

	s.OnTerminating(func(_ error) {
		s.server.Shutdown(nil)
	})

	s.logger.Info("starting consumer sidecar", zap.String("listen_addr", s.listenAddr))
	s.server.Launch(s.listenAddr)
}

func (s *Sidecar) healthCheck(ctx context.Context) (isReady bool, out interface{}, err error) {
	return true, nil, nil
}
