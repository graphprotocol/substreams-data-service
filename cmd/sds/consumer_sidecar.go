package main

import (
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/cli/sflags"
	"github.com/streamingfast/logging"

	"github.com/graphprotocol/substreams-data-service/consumer/sidecar"
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
	}),
)

func runConsumerSidecar(cmd *cobra.Command, args []string) error {
	listenAddr := sflags.MustGetString(cmd, "grpc-listen-addr")

	app := NewApplication(cmd.Context())

	sidecarServer := sidecar.New(listenAddr, consumerLog)
	app.SuperviseAndStart(sidecarServer)

	return app.WaitForTermination(consumerLog, 0*time.Second, 30*time.Second)
}
