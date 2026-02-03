package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/cli/sflags"
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
			flags.Uint64("chain-id", 1337, "Chain ID for the Anvil network")
			flags.Uint64("block-time", 1, "Block time in seconds for Anvil")
		}),
		ConfigureVersion(version),
		OnCommandErrorLogAndExit(zlog),
	)
}

func run(cmd *cobra.Command, args []string) error {
	chainID := sflags.MustGetUint64(cmd, "chain-id")
	blockTime := sflags.MustGetUint64(cmd, "block-time")

	// Validate Docker is accessible
	fmt.Println("Checking Docker availability...")
	if err := checkDocker(); err != nil {
		return fmt.Errorf("Docker is not available: %w\nPlease ensure Docker is installed and running", err)
	}
	fmt.Println("Docker is available")

	// Build options
	opts := []devenv.Option{
		devenv.WithChainID(chainID),
		devenv.WithBlockTime(blockTime),
	}

	fmt.Printf("\nStarting Substreams Data Service development environment...\n")
	fmt.Printf("  Chain ID: %d\n", chainID)
	fmt.Printf("  Block time: %d second(s)\n", blockTime)
	fmt.Println()

	// Start the environment
	ctx := context.Background()
	env, err := devenv.Start(ctx, opts...)
	if err != nil {
		return err
	}

	// Print how to stop
	fmt.Println("\nPress Ctrl+C to shut down the environment")

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down development environment...")
	devenv.Shutdown()
	fmt.Println("Shutdown complete")

	_ = env // Used above, silence unused warning
	return nil
}

// checkDocker verifies that Docker is accessible
func checkDocker() error {
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
