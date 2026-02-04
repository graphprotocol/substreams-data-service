package main

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/cli/sflags"
	"github.com/streamingfast/logging"

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
	}),
)

func runProviderSidecar(cmd *cobra.Command, args []string) error {
	listenAddr := sflags.MustGetString(cmd, "grpc-listen-addr")

	app := NewApplication(cmd.Context())

	sidecarServer := sidecar.New(listenAddr, providerLog)
	app.SuperviseAndStart(sidecarServer)

	return app.WaitForTermination(providerLog, 0*time.Second, 30*time.Second)
}
