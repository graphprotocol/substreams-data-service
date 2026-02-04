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

	"github.com/graphprotocol/substreams-data-service/consumer/sidecar"
	"github.com/graphprotocol/substreams-data-service/horizon"
)

var consumerLog, _ = logging.PackageLogger("consumer", "github.com/graphprotocol/substreams-data-service/cmd/sds@consumer")

var consumerSidecarCmd = Command(
	runConsumerSidecar,
	"sidecar",
	"Start the consumer sidecar gRPC server",
	Description(`
		Starts the consumer sidecar which handles payment session management
		and RAV signing for data consumers.

		The sidecar exposes:
		- ConsumerSidecarService: Called by the substreams client to manage payment sessions
	`),
	Flags(func(flags *pflag.FlagSet) {
		flags.String("grpc-listen-addr", ":9002", "gRPC server listen address")
		flags.String("signer-key", "", "Private key for signing RAVs (hex, required)")
		flags.Uint64("chain-id", 1337, "Chain ID for EIP-712 domain")
		flags.String("collector-address", "", "Collector contract address for EIP-712 domain (required)")
	}),
)

func runConsumerSidecar(cmd *cobra.Command, args []string) error {
	listenAddr := sflags.MustGetString(cmd, "grpc-listen-addr")
	signerKeyHex := sflags.MustGetString(cmd, "signer-key")
	chainID := sflags.MustGetUint64(cmd, "chain-id")
	collectorHex := sflags.MustGetString(cmd, "collector-address")

	cli.Ensure(signerKeyHex != "", "<signer-key> is required")
	signerKey, err := eth.NewPrivateKey(signerKeyHex)
	cli.NoError(err, "invalid <signer-key> %q", signerKeyHex)

	cli.Ensure(collectorHex != "", "<collector-address> is required")
	collectorAddr, err := eth.NewAddress(collectorHex)
	cli.NoError(err, "invalid <collector-address> %q", collectorHex)

	config := &sidecar.Config{
		ListenAddr: listenAddr,
		SignerKey:  signerKey,
		Domain:     horizon.NewDomain(chainID, collectorAddr),
	}

	app := NewApplication(cmd.Context())

	sidecarServer := sidecar.New(config, consumerLog)
	app.SuperviseAndStart(sidecarServer)

	return app.WaitForTermination(consumerLog, 0*time.Second, 30*time.Second)
}
