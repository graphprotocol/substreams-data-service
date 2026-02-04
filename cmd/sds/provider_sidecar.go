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
	sidecarlib "github.com/graphprotocol/substreams-data-service/sidecar"
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

		Pricing configuration should be provided via a YAML file with the following format:
		  price_per_block: "0.000001"   # Price per processed block in GRT
		  price_per_byte: "0.0000000001" # Price per byte transferred in GRT
	`),
	Flags(func(flags *pflag.FlagSet) {
		flags.String("grpc-listen-addr", ":9001", "gRPC server listen address")
		flags.String("service-provider", "", "Service provider address (required)")
		flags.Uint64("chain-id", 1337, "Chain ID for EIP-712 domain")
		flags.String("collector-address", "", "Collector contract address for EIP-712 domain (required)")
		flags.String("escrow-address", "", "PaymentsEscrow contract address for balance queries (required)")
		flags.String("rpc-endpoint", "", "Ethereum RPC endpoint for on-chain queries (required)")
		flags.String("pricing-config", "", "Path to pricing configuration YAML file (uses defaults if not provided)")
	}),
)

func runProviderSidecar(cmd *cobra.Command, args []string) error {
	listenAddr := sflags.MustGetString(cmd, "grpc-listen-addr")
	serviceProviderHex := sflags.MustGetString(cmd, "service-provider")
	chainID := sflags.MustGetUint64(cmd, "chain-id")
	collectorHex := sflags.MustGetString(cmd, "collector-address")
	escrowHex := sflags.MustGetString(cmd, "escrow-address")
	rpcEndpoint := sflags.MustGetString(cmd, "rpc-endpoint")
	pricingConfigPath := sflags.MustGetString(cmd, "pricing-config")

	cli.Ensure(serviceProviderHex != "", "<service-provider> is required")
	serviceProviderAddr, err := eth.NewAddress(serviceProviderHex)
	cli.NoError(err, "invalid <service-provider> %q", serviceProviderHex)

	cli.Ensure(collectorHex != "", "<collector-address> is required")
	collectorAddr, err := eth.NewAddress(collectorHex)
	cli.NoError(err, "invalid <collector-address> %q", collectorHex)

	cli.Ensure(escrowHex != "", "<escrow-address> is required")
	escrowAddr, err := eth.NewAddress(escrowHex)
	cli.NoError(err, "invalid <escrow-address> %q", escrowHex)

	cli.Ensure(rpcEndpoint != "", "<rpc-endpoint> is required")

	// Load pricing configuration
	var pricingConfig *sidecarlib.PricingConfig
	if pricingConfigPath != "" {
		pricingConfig, err = sidecarlib.LoadPricingConfig(pricingConfigPath)
		cli.NoError(err, "failed to load pricing config from %q", pricingConfigPath)
	} else {
		pricingConfig = sidecarlib.DefaultPricingConfig()
	}

	config := &sidecar.Config{
		ListenAddr:      listenAddr,
		ServiceProvider: serviceProviderAddr,
		Domain:          horizon.NewDomain(chainID, collectorAddr),
		CollectorAddr:   collectorAddr,
		EscrowAddr:      escrowAddr,
		RPCEndpoint:     rpcEndpoint,
		PricingConfig:   pricingConfig,
		AcceptedSigners: nil, // Will be configured dynamically
	}

	app := NewApplication(cmd.Context())

	sidecarServer := sidecar.New(config, providerLog)
	app.SuperviseAndStart(sidecarServer)

	return app.WaitForTermination(providerLog, 0*time.Second, 30*time.Second)
}
