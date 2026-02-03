package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/logging"
	"go.uber.org/zap"

	"github.com/graphprotocol/substreams-data-service/horizon/devenv"
)

var zlog, _ = logging.PackageLogger("sds-devenv", "github.com/graphprotocol/substreams-data-service/cmd/sds-devenv")
var version = "dev"

func init() {
	logging.InstantiateLoggers(logging.WithDefaultLevel(zap.ErrorLevel))
}

func main() {
	Run(
		"sds-devenv",
		"Start a development environment for Substreams Data Service",
		Execute(run),
		Description(`
			Starts a local Anvil node and deploys all necessary Graph Protocol contracts
			for testing the Substreams Data Service.

			The environment includes:
			- MockGRTToken: ERC20 token for testing
			- MockController: Contract registry
			- MockStaking: Provision management
			- PaymentsEscrow: Original Graph escrow contract
			- GraphPayments: Original payment distribution contract
			- GraphTallyCollector: Original RAV verification contract
			- SubstreamsDataService: Data service contract

			Press Ctrl+C to shut down the environment.
		`),
		Flags(func(flags *pflag.FlagSet) {
			flags.Bool("force-build", false, "Force rebuild contract artifacts even if they exist")
			flags.Uint64("chain-id", 1337, "Chain ID for the Anvil network")
			flags.Uint64("block-time", 1, "Block time in seconds for Anvil")
		}),
		ConfigureVersion(version),
		OnCommandErrorLogAndExit(zlog),
	)
}

func run(cmd *cobra.Command, args []string) error {
	forceBuild, _ := cmd.Flags().GetBool("force-build")
	chainID, _ := cmd.Flags().GetUint64("chain-id")
	blockTime, _ := cmd.Flags().GetUint64("block-time")

	// Build options
	opts := []devenv.Option{
		devenv.WithChainID(chainID),
		devenv.WithBlockTime(blockTime),
	}
	if forceBuild {
		opts = append(opts, devenv.WithForceBuild())
	}

	zlog.Info("starting Substreams Data Service development environment",
		zap.Uint64("chain_id", chainID),
		zap.Uint64("block_time", blockTime),
		zap.Bool("force_build", forceBuild),
	)

	// Start the environment
	ctx := context.Background()
	env, err := devenv.Start(ctx, opts...)
	if err != nil {
		return err
	}

	// Print summary for easy access
	zlog.Info("development environment is running",
		zap.String("rpc_url", env.RPCURL),
		zap.Uint64("chain_id", env.ChainID),
		zap.Stringer("collector", env.Collector.Address),
		zap.Stringer("data_service", env.DataService.Address),
	)

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	zlog.Info("press Ctrl+C to shut down")
	<-sigCh

	zlog.Info("shutting down development environment...")
	devenv.Shutdown()
	zlog.Info("shutdown complete")

	return nil
}
