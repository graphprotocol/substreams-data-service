package main

import (
	"math/big"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/graphprotocol/substreams-data-service/horizon"
	commonv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/common/v1"
	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
	"github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1/providerv1connect"
	"github.com/graphprotocol/substreams-data-service/sidecar"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/streamingfast/cli"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/cli/sflags"
	"github.com/streamingfast/eth-go"
	"go.uber.org/zap"
)

var providerFakeOperatorCmd = Command(
	runProviderFakeOperator,
	"fake-operator",
	"Simulate a data provider operator interacting with the provider sidecar",
	Description(`
		Simulates a data provider (like substreams-tier2) that:
		1. Validates a payment RAV from a consumer
		2. Reports usage as data is streamed
		3. Ends the session

		This is useful for testing the provider sidecar without running actual provider services.
	`),
	Flags(func(flags *pflag.FlagSet) {
		flags.String("provider-sidecar-addr", "http://localhost:9001", "Provider sidecar address")
		flags.String("signer-private-key", "", "Private key for signing test RAVs (hex, required)")
		flags.Uint64("chain-id", 1337, "Chain ID for EIP-712 domain")
		flags.String("collector-address", "", "Collector contract address for EIP-712 domain (required)")
		flags.String("payer-address", "", "Payer address (required)")
		flags.String("service-provider-address", "", "Service provider address (required)")
		flags.String("data-service-address", "", "Data service contract address (required)")
		flags.Uint64("blocks-to-simulate", 100, "Number of blocks to simulate streaming")
		flags.Uint64("bytes-per-block", 1000, "Simulated bytes transferred per block")
		flags.Uint64("batch-size", 10, "Number of blocks per usage report")
		flags.String("price-per-block", "0.001", "Price per block in GRT for cost calculation")
		flags.Duration("delay-between-batches", 500*time.Millisecond, "Delay between batch reports")
	}),
)

func runProviderFakeOperator(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	sidecarAddr := sflags.MustGetString(cmd, "provider-sidecar-addr")
	signerKeyHex := sflags.MustGetString(cmd, "signer-private-key")
	chainID := sflags.MustGetUint64(cmd, "chain-id")
	collectorHex := sflags.MustGetString(cmd, "collector-address")
	payerHex := sflags.MustGetString(cmd, "payer-address")
	serviceProviderHex := sflags.MustGetString(cmd, "service-provider-address")
	dataServiceHex := sflags.MustGetString(cmd, "data-service-address")
	blocksToSimulate := sflags.MustGetUint64(cmd, "blocks-to-simulate")
	bytesPerBlock := sflags.MustGetUint64(cmd, "bytes-per-block")
	batchSize := sflags.MustGetUint64(cmd, "batch-size")
	pricePerBlockStr := sflags.MustGetString(cmd, "price-per-block")
	delayBetweenBatches := sflags.MustGetDuration(cmd, "delay-between-batches")

	cli.Ensure(signerKeyHex != "", "<signer-private-key> is required")
	signerKey, err := eth.NewPrivateKey(signerKeyHex)
	cli.NoError(err, "invalid <signer-private-key> %q", signerKeyHex)

	cli.Ensure(collectorHex != "", "<collector-address> is required")
	collectorAddr, err := eth.NewAddress(collectorHex)
	cli.NoError(err, "invalid <collector-address> %q", collectorHex)

	cli.Ensure(payerHex != "", "<payer-address> is required")
	payer, err := eth.NewAddress(payerHex)
	cli.NoError(err, "invalid <payer-address> %q", payerHex)

	cli.Ensure(serviceProviderHex != "", "<service-provider-address> is required")
	serviceProvider, err := eth.NewAddress(serviceProviderHex)
	cli.NoError(err, "invalid <service-provider-address> %q", serviceProviderHex)

	cli.Ensure(dataServiceHex != "", "<data-service-address> is required")
	dataService, err := eth.NewAddress(dataServiceHex)
	cli.NoError(err, "invalid <data-service-address> %q", dataServiceHex)

	// Parse price per block (in GRT)
	pricePerBlock, ok := new(big.Float).SetString(pricePerBlockStr)
	cli.Ensure(ok, "invalid <price-per-block> %q", pricePerBlockStr)

	// Convert to wei (multiply by 10^18)
	weiMultiplier := new(big.Float).SetInt(big.NewInt(1e18))
	priceWei, _ := new(big.Float).Mul(pricePerBlock, weiMultiplier).Int(nil)

	domain := horizon.NewDomain(chainID, collectorAddr)

	logger := providerLog
	logger.Info("starting fake provider client",
		zap.String("sidecar_addr", sidecarAddr),
		zap.Stringer("payer", payer),
		zap.Stringer("service_provider", serviceProvider),
		zap.Stringer("data_service", dataService),
		zap.Uint64("blocks_to_simulate", blocksToSimulate),
		zap.Uint64("batch_size", batchSize),
		zap.String("price_per_block", pricePerBlockStr),
	)

	// Create client
	client := providerv1connect.NewProviderSidecarServiceClient(
		http.DefaultClient,
		sidecarAddr,
	)

	// Step 1: Create an initial RAV and validate payment
	logger.Info("Step 1: Creating initial RAV and validating payment")

	initialRAV, err := signRAV(
		domain,
		signerKey,
		[32]byte{}, // Zero collection ID for new session
		payer,
		dataService,
		serviceProvider,
		uint64(time.Now().UnixNano()),
		big.NewInt(0), // Zero initial value
		nil,
	)
	cli.NoError(err, "failed to sign initial RAV")

	validateResp, err := client.ValidatePayment(ctx, connect.NewRequest(&providerv1.ValidatePaymentRequest{
		PaymentRav: sidecar.HorizonSignedRAVToProto(initialRAV),
	}))
	cli.NoError(err, "failed to validate payment")

	if !validateResp.Msg.Valid {
		logger.Error("payment validation failed",
			zap.String("reason", validateResp.Msg.RejectionReason),
		)
		cli.Quit("payment validation failed: %s", validateResp.Msg.RejectionReason)
	}

	sessionID := validateResp.Msg.SessionId
	logger.Info("payment validated, session created",
		zap.String("session_id", sessionID),
		zap.Bool("valid", validateResp.Msg.Valid),
	)

	if validateResp.Msg.AvailableBalance != nil {
		logger.Info("escrow balance",
			zap.String("available", validateResp.Msg.AvailableBalance.ToNative().String()),
		)
	}

	// Step 2: Simulate streaming data and reporting usage
	logger.Info("Step 2: Simulating data streaming")
	var totalBlocks, totalBytes, totalRequests uint64
	totalCost := big.NewInt(0)

	for blocksStreamed := uint64(0); blocksStreamed < blocksToSimulate; blocksStreamed += batchSize {
		// Calculate batch size (may be smaller for last batch)
		currentBatch := batchSize
		if blocksStreamed+batchSize > blocksToSimulate {
			currentBatch = blocksToSimulate - blocksStreamed
		}

		bytes := currentBatch * bytesPerBlock
		requests := uint64(1)
		cost := new(big.Int).Mul(priceWei, big.NewInt(int64(currentBatch)))

		usageResp, err := client.ReportUsage(ctx, connect.NewRequest(&providerv1.ReportUsageRequest{
			SessionId: sessionID,
			Usage: &commonv1.Usage{
				BlocksProcessed:  currentBatch,
				BytesTransferred: bytes,
				Requests:         requests,
				Cost:             commonv1.BigIntFromNative(cost),
			},
		}))
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

		logger.Debug("batch streamed",
			zap.Uint64("blocks_in_batch", currentBatch),
			zap.Uint64("total_blocks", totalBlocks),
			zap.Bool("rav_updated", usageResp.Msg.RavUpdated),
		)

		// Delay between batches to simulate real streaming
		if delayBetweenBatches > 0 && blocksStreamed+batchSize < blocksToSimulate {
			time.Sleep(delayBetweenBatches)
		}
	}

	// Step 3: Check session status
	logger.Info("Step 3: Checking session status")
	statusResp, err := client.GetSessionStatus(ctx, connect.NewRequest(&providerv1.GetSessionStatusRequest{
		SessionId: sessionID,
	}))
	cli.NoError(err, "failed to get session status")

	if statusResp.Msg.Active {
		logger.Info("session status",
			zap.Bool("active", statusResp.Msg.Active),
		)
		if statusResp.Msg.PaymentStatus != nil {
			logger.Info("payment status",
				zap.String("accumulated_usage", statusResp.Msg.PaymentStatus.AccumulatedUsageValue.ToNative().String()),
				zap.String("escrow_balance", statusResp.Msg.PaymentStatus.EscrowBalance.ToNative().String()),
				zap.Bool("funds_sufficient", statusResp.Msg.PaymentStatus.FundsSufficient),
				zap.Uint64("estimated_blocks_remaining", statusResp.Msg.PaymentStatus.EstimatedBlocksRemaining),
			)
		}
	}

	// Step 4: End session
	logger.Info("Step 4: Ending session")
	endResp, err := client.EndSession(ctx, connect.NewRequest(&providerv1.EndSessionRequest{
		SessionId: sessionID,
		FinalUsage: &commonv1.Usage{
			BlocksProcessed:  0, // Already reported
			BytesTransferred: 0,
			Requests:         0,
			Cost:             commonv1.BigIntFromNative(big.NewInt(0)),
		},
		Reason: commonv1.EndReason_END_REASON_COMPLETE,
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

	if endResp.Msg.TotalValue != nil {
		logger.Info("total value",
			zap.String("wei", endResp.Msg.TotalValue.ToNative().String()),
		)
	}

	return nil
}

// signRAV creates a signed RAV using the provided private key
func signRAV(
	domain *horizon.Domain,
	key *eth.PrivateKey,
	collectionID [32]byte,
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
	return horizon.Sign(domain, rav, key)
}
