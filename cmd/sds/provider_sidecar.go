package main

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/streamingfast/cli"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/cli/sflags"
	"github.com/streamingfast/eth-go"
	"github.com/streamingfast/logging"

	"github.com/graphprotocol/substreams-data-service/horizon"
	"github.com/graphprotocol/substreams-data-service/provider/sidecar"
)

var providerLog, _ = logging.PackageLogger("provider", "github.com/graphprotocol/substreams-data-service/cmd/sds@provider")

var providerSidecarCmd = Command(
	runProviderSidecar,
	"sidecar",
	"Start the provider sidecar gRPC server",
	Description(`
		Starts the provider sidecar which handles payment validation and usage
		tracking for data providers.

		The sidecar exposes two services:
		- ProviderSidecarService: Called by the data provider to validate payments and report usage
		- PaymentGatewayService: Called by consumer sidecars for session management and RAV exchange
	`),
	Flags(func(flags *pflag.FlagSet) {
		flags.String("grpc-listen-addr", ":9001", "gRPC server listen address")
		flags.String("service-provider", "", "Service provider address (required)")
		flags.Uint64("chain-id", 1337, "Chain ID for EIP-712 domain")
		flags.String("collector-address", "", "Collector contract address for EIP-712 domain (required)")
	}),
)

func runProviderSidecar(cmd *cobra.Command, args []string) error {
	listenAddr := sflags.MustGetString(cmd, "grpc-listen-addr")
	serviceProviderHex := sflags.MustGetString(cmd, "service-provider")
	chainID := sflags.MustGetUint64(cmd, "chain-id")
	collectorHex := sflags.MustGetString(cmd, "collector-address")

	cli.Ensure(serviceProviderHex != "", "<service-provider> is required")
	serviceProviderAddr, err := eth.NewAddress(serviceProviderHex)
	cli.NoError(err, "invalid <service-provider> %q", serviceProviderHex)

	cli.Ensure(collectorHex != "", "<collector-address> is required")
	collectorAddr, err := eth.NewAddress(collectorHex)
	cli.NoError(err, "invalid <collector-address> %q", collectorHex)

	config := &sidecar.Config{
		ListenAddr:      listenAddr,
		ServiceProvider: serviceProviderAddr,
		Domain:          horizon.NewDomain(chainID, collectorAddr),
		AcceptedSigners: nil, // Will be configured dynamically
	}

	app := NewApplication(cmd.Context())

	sidecarServer := sidecar.New(config, providerLog)
	app.SuperviseAndStart(sidecarServer)

	return app.WaitForTermination(providerLog, 0*time.Second, 30*time.Second)
}
