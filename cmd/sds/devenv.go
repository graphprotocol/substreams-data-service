package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/graphprotocol/substreams-data-service/horizon/devenv"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/cli/sflags"
)

var devenvCmd = Command(
	runDevenv,
	"devenv",
	"Start a local development environment with Graph Protocol contracts",
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
	}),
)

// consoleReporter prints progress messages to the console
type consoleReporter struct{}

func (consoleReporter) ReportProgress(message string) {
	fmt.Println(message)
}

func runDevenv(cmd *cobra.Command, args []string) error {
	chainID := sflags.MustGetUint64(cmd, "chain-id")

	// Validate Docker is accessible
	fmt.Println("Checking Docker availability...")
	if err := checkDocker(); err != nil {
		return fmt.Errorf("Docker is not available: %w\nPlease ensure Docker is installed and running", err)
	}

	fmt.Printf("\nStarting Substreams Data Service development environment...\n")
	fmt.Printf("  Chain ID: %d\n", chainID)
	fmt.Println()

	// Build options
	opts := []devenv.Option{
		devenv.WithChainID(chainID),
		devenv.WithReporter(consoleReporter{}),
	}

	// Start the environment
	ctx := context.Background()
	env, err := devenv.Start(ctx, opts...)
	if err != nil {
		return err
	}

	// Print environment info
	env.PrintInfo(os.Stdout)

	// Print how to stop
	fmt.Println("\nPress Ctrl+C to shut down the environment")

	// Wait for interrupt
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Println("\nShutting down development environment...")
	devenv.Shutdown()
	fmt.Println("Shutdown complete")

	return nil
}

// checkDocker verifies that Docker is accessible
func checkDocker() error {
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
