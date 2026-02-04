package sidecar

import (
	"context"
	"math/big"
	"net/http"

	"connectrpc.com/connect"
	"github.com/graphprotocol/substreams-data-service/horizon"
	"github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/consumer/v1/consumerv1connect"
	"github.com/graphprotocol/substreams-data-service/sidecar"
	"github.com/streamingfast/dgrpc/server"
	"github.com/streamingfast/dgrpc/server/connectrpc"
	"github.com/streamingfast/eth-go"
	"github.com/streamingfast/shutter"
	"go.uber.org/zap"
)

var _ consumerv1connect.ConsumerSidecarServiceHandler = (*Sidecar)(nil)

type Sidecar struct {
	*shutter.Shutter

	listenAddr string
	logger     *zap.Logger
	server     *connectrpc.ConnectWebServer

	// Session management
	sessions *sidecar.SessionManager

	// Signing configuration
	signerKey *eth.PrivateKey
	domain    *horizon.Domain

	// Provider gateway endpoint (set during Init)
	// In production, this would be dynamically determined
}

type Config struct {
	ListenAddr string
	SignerKey  *eth.PrivateKey
	Domain     *horizon.Domain
}

func New(config *Config, logger *zap.Logger) *Sidecar {
	return &Sidecar{
		Shutter:    shutter.New(),
		listenAddr: config.ListenAddr,
		logger:     logger,
		sessions:   sidecar.NewSessionManager(),
		signerKey:  config.SignerKey,
		domain:     config.Domain,
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

// signRAV creates a signed RAV for the given parameters
func (s *Sidecar) signRAV(
	collectionID horizon.CollectionID,
	payer, dataService, serviceProvider eth.Address,
	timestampNs uint64,
	valueAggregate *big.Int,
	metadata []byte,
) (*horizon.SignedRAV, error) {
	rav := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           payer,
		DataService:     dataService,
		ServiceProvider: serviceProvider,
		TimestampNs:     timestampNs,
		ValueAggregate:  valueAggregate,
		Metadata:        metadata,
	}

	return horizon.Sign(s.domain, rav, s.signerKey)
}
