package main

import (
	"context"
	"math/big"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/streamingfast/cli"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/cli/sflags"
	"github.com/streamingfast/eth-go"
	"go.uber.org/zap"

	commonv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/common/v1"
	consumerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/consumer/v1"
	"github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/consumer/v1/consumerv1connect"
)

var consumerFakeClientCmd = Command(
	runConsumerFakeClient,
	"fake-client",
	"Simulate a substreams client connecting to the consumer sidecar",
	Description(`
		Simulates a substreams client that:
		1. Initializes a payment session with the consumer sidecar
		2. Simulates data consumption by reporting usage
		3. Ends the session

		This is useful for testing the consumer sidecar without running actual substreams.
	`),
	Flags(func(flags *pflag.FlagSet) {
		flags.String("consumer-sidecar-addr", "http://localhost:9002", "Consumer sidecar address")
		flags.String("provider-endpoint", "localhost:9000", "Provider endpoint to pass in Init")
		flags.String("payer-address", "", "Payer address (required)")
		flags.String("receiver-address", "", "Receiver/service provider address (required)")
		flags.String("data-service-address", "", "Data service contract address (required)")
		flags.Uint64("blocks-to-simulate", 100, "Number of blocks to simulate processing")
		flags.Uint64("bytes-per-block", 1000, "Simulated bytes transferred per block")
		flags.Uint64("batch-size", 10, "Number of blocks per usage report")
		flags.String("price-per-block", "0.001", "Price per block in GRT for cost calculation")
		flags.Duration("delay-between-batches", 500*time.Millisecond, "Delay between batch reports")
	}),
)

func runConsumerFakeClient(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	sidecarAddr := sflags.MustGetString(cmd, "consumer-sidecar-addr")
	providerEndpoint := sflags.MustGetString(cmd, "provider-endpoint")
	payerHex := sflags.MustGetString(cmd, "payer-address")
	receiverHex := sflags.MustGetString(cmd, "receiver-address")
	dataServiceHex := sflags.MustGetString(cmd, "data-service-address")
	blocksToSimulate := sflags.MustGetUint64(cmd, "blocks-to-simulate")
	bytesPerBlock := sflags.MustGetUint64(cmd, "bytes-per-block")
	batchSize := sflags.MustGetUint64(cmd, "batch-size")
	pricePerBlockStr := sflags.MustGetString(cmd, "price-per-block")
	delayBetweenBatches := sflags.MustGetDuration(cmd, "delay-between-batches")

	cli.Ensure(payerHex != "", "<payer-address> is required")
	payer, err := eth.NewAddress(payerHex)
	cli.NoError(err, "invalid <payer-address> %q", payerHex)

	cli.Ensure(receiverHex != "", "<receiver-address> is required")
	receiver, err := eth.NewAddress(receiverHex)
	cli.NoError(err, "invalid <receiver-address> %q", receiverHex)

	cli.Ensure(dataServiceHex != "", "<data-service-address> is required")
	dataService, err := eth.NewAddress(dataServiceHex)
	cli.NoError(err, "invalid <data-service-address> %q", dataServiceHex)

	// Parse price per block (in GRT)
	pricePerBlock, ok := new(big.Float).SetString(pricePerBlockStr)
	cli.Ensure(ok, "invalid <price-per-block> %q", pricePerBlockStr)

	// Convert to wei (multiply by 10^18)
	weiMultiplier := new(big.Float).SetInt(big.NewInt(1e18))
	priceWei, _ := new(big.Float).Mul(pricePerBlock, weiMultiplier).Int(nil)

	logger := consumerLog
	logger.Info("starting fake client",
		zap.String("sidecar_addr", sidecarAddr),
		zap.String("provider_endpoint", providerEndpoint),
		zap.Stringer("payer", payer),
		zap.Stringer("receiver", receiver),
		zap.Stringer("data_service", dataService),
		zap.Uint64("blocks_to_simulate", blocksToSimulate),
		zap.Uint64("batch_size", batchSize),
		zap.String("price_per_block", pricePerBlockStr),
	)

	// Create client
	client := consumerv1connect.NewConsumerSidecarServiceClient(
		http.DefaultClient,
		sidecarAddr,
	)

	// Step 1: Initialize session
	logger.Info("Step 1: Initializing session")
	initResp, err := client.Init(ctx, connect.NewRequest(&consumerv1.InitRequest{
		EscrowAccount: &commonv1.EscrowAccount{
			Payer:       commonv1.AddressFromEth(payer),
			Receiver:    commonv1.AddressFromEth(receiver),
			DataService: commonv1.AddressFromEth(dataService),
		},
		ProviderEndpoint: providerEndpoint,
	}))
	cli.NoError(err, "failed to initialize session")

	sessionID := initResp.Msg.Session.SessionId
	logger.Info("session initialized",
		zap.String("session_id", sessionID),
	)

	if initResp.Msg.PaymentRav != nil && initResp.Msg.PaymentRav.Rav != nil {
		logger.Info("received initial RAV",
			zap.String("value", initResp.Msg.PaymentRav.Rav.ValueAggregate.ToNative().String()),
		)
	}

	// Step 2: Simulate data consumption
	logger.Info("Step 2: Simulating data consumption")
	var totalBlocks, totalBytes, totalRequests uint64
	totalCost := big.NewInt(0)

	for blocksProcessed := uint64(0); blocksProcessed < blocksToSimulate; blocksProcessed += batchSize {
		// Calculate batch size (may be smaller for last batch)
		currentBatch := batchSize
		if blocksProcessed+batchSize > blocksToSimulate {
			currentBatch = blocksToSimulate - blocksProcessed
		}

		bytes := currentBatch * bytesPerBlock
		requests := uint64(1)
		cost := new(big.Int).Mul(priceWei, big.NewInt(int64(currentBatch)))

		usageResp, err := reportUsage(ctx, client, sessionID, currentBatch, bytes, requests, cost, logger)
		cli.NoError(err, "failed to report usage")

		totalBlocks += currentBatch
		totalBytes += bytes
		totalRequests += requests
		totalCost.Add(totalCost, cost)

		if !usageResp.Msg.ShouldContinue {
			logger.Warn("sidecar requested to stop",
				zap.String("reason", usageResp.Msg.StopReason),
			)
			break
		}

		if usageResp.Msg.UpdatedRav != nil && usageResp.Msg.UpdatedRav.Rav != nil {
			logger.Debug("batch processed",
				zap.Uint64("blocks_in_batch", currentBatch),
				zap.Uint64("total_blocks", totalBlocks),
				zap.String("updated_rav_value", usageResp.Msg.UpdatedRav.Rav.ValueAggregate.ToNative().String()),
			)
		} else {
			logger.Debug("batch processed",
				zap.Uint64("blocks_in_batch", currentBatch),
				zap.Uint64("total_blocks", totalBlocks),
			)
		}

		// Delay between batches to simulate real streaming
		if delayBetweenBatches > 0 && blocksProcessed+batchSize < blocksToSimulate {
			time.Sleep(delayBetweenBatches)
		}
	}

	// Step 3: End session
	logger.Info("Step 3: Ending session")
	endResp, err := client.EndSession(ctx, connect.NewRequest(&consumerv1.EndSessionRequest{
		SessionId: sessionID,
		FinalUsage: &commonv1.Usage{
			BlocksProcessed:  0, // Already reported
			BytesTransferred: 0,
			Requests:         0,
			Cost:             commonv1.BigIntFromNative(big.NewInt(0)),
		},
	}))
	cli.NoError(err, "failed to end session")

	logger.Info("session ended successfully",
		zap.String("session_id", sessionID),
		zap.Uint64("total_blocks", totalBlocks),
		zap.Uint64("total_bytes", totalBytes),
		zap.Uint64("total_requests", totalRequests),
		zap.String("total_cost", totalCost.String()),
	)

	if endResp.Msg.FinalRav != nil && endResp.Msg.FinalRav.Rav != nil {
		logger.Info("final RAV",
			zap.String("value", endResp.Msg.FinalRav.Rav.ValueAggregate.ToNative().String()),
		)
	}

	if endResp.Msg.TotalUsage != nil {
		logger.Info("total usage reported by sidecar",
			zap.Uint64("blocks", endResp.Msg.TotalUsage.BlocksProcessed),
			zap.Uint64("bytes", endResp.Msg.TotalUsage.BytesTransferred),
			zap.Uint64("requests", endResp.Msg.TotalUsage.Requests),
		)
	}

	return nil
}

func reportUsage(
	ctx context.Context,
	client consumerv1connect.ConsumerSidecarServiceClient,
	sessionID string,
	blocks, bytes, requests uint64,
	cost *big.Int,
	logger *zap.Logger,
) (*connect.Response[consumerv1.ReportUsageResponse], error) {
	return client.ReportUsage(ctx, connect.NewRequest(&consumerv1.ReportUsageRequest{
		SessionId: sessionID,
		Usage: &commonv1.Usage{
			BlocksProcessed:  blocks,
			BytesTransferred: bytes,
			Requests:         requests,
			Cost:             commonv1.BigIntFromNative(cost),
		},
	}))
}
