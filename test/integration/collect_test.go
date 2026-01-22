package integration

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/streamingfast/eth-go"
	horizon "github.com/streamingfast/horizon-go"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type testContext = context.Context

// TestCollectRAV tests the full collect() flow with escrow
func TestCollectRAV(t *testing.T) {
	env := SetupEnv(t)
	zlog.Info("starting TestCollectRAV", zap.Uint64("chain_id", env.ChainID))

	// Setup: Fund payer's escrow with GRT tokens
	tokensToDeposit := new(big.Int)
	tokensToDeposit.SetString("10000000000000000000000", 10) // 10,000 GRT

	// Mint GRT to payer
	zlog.Debug("minting GRT to payer", zap.String("payer", env.PayerAddr.Pretty()), zap.String("amount", tokensToDeposit.String()))
	err := callMintGRT(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.GRTToken, env.PayerAddr, tokensToDeposit, env.ABIs.GRTToken)
	require.NoError(t, err, "Failed to mint GRT")

	// Approve escrow to spend GRT
	zlog.Debug("approving GRT for escrow", zap.String("escrow", env.PaymentsEscrow.Pretty()))
	err = callApproveGRT(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.GRTToken, env.PaymentsEscrow, tokensToDeposit, env.ABIs.GRTToken)
	require.NoError(t, err, "Failed to approve GRT")

	// Deposit to escrow (using 3-level mapping: payer -> collector -> receiver)
	zlog.Debug("depositing to escrow", zap.String("amount", tokensToDeposit.String()))
	err = callDepositEscrow(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.PaymentsEscrow, env.CollectorAddress, env.ServiceProviderAddr, tokensToDeposit, env.ABIs.Escrow)
	require.NoError(t, err, "Failed to deposit to escrow")

	// Set up SubstreamsDataService: set provision tokens range (min = 0 for testing)
	err = callSetProvisionTokensRange(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.SubstreamsDataService, big.NewInt(0), env.ABIs.DataService)
	require.NoError(t, err, "Failed to set provision tokens range")

	// Setup: Set provision for service provider in staking contract
	provisionTokens := new(big.Int)
	provisionTokens.SetString("1000000000000000000000", 10) // 1,000 GRT
	maxVerifierCut := uint32(0)
	thawingPeriod := uint64(0)
	zlog.Debug("setting provision", zap.String("service_provider", env.ServiceProviderAddr.Pretty()), zap.String("amount", provisionTokens.String()))
	err = callSetProvision(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.Staking, env.ServiceProviderAddr, env.SubstreamsDataService, provisionTokens, maxVerifierCut, thawingPeriod, env.ABIs.Staking)
	require.NoError(t, err, "Failed to set provision")

	// Register service provider with SubstreamsDataService
	err = callRegisterWithDataService(env.ctx, env.rpcURL, env.ServiceProviderKey, env.ChainID, env.SubstreamsDataService, env.ServiceProviderAddr, env.ServiceProviderAddr, env.ABIs.DataService)
	require.NoError(t, err, "Failed to register with data service")

	// Create a dedicated signer key for signing RAVs
	// In the original contract, self-authorization is NOT supported - we must explicitly authorize
	signerKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	signerAddr := signerKey.PublicKey().Address()

	// Authorize the signer (payer authorizes the signer to sign RAVs on their behalf)
	zlog.Debug("authorizing signer for RAV signing", zap.Stringer("payer", env.PayerAddr), zap.Stringer("signer", signerAddr))
	err = callAuthorizeSigner(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.CollectorAddress, signerKey, env.ABIs.Collector)
	require.NoError(t, err, "Failed to authorize signer")

	// Create domain
	zlog.Debug("creating EIP-712 domain", zap.Uint64("chain_id", env.ChainID), zap.String("verifying_contract", env.CollectorAddress.Pretty()))
	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	// Create RAV
	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")[:])

	valueAggregate := big.NewInt(1000000000000000000) // 1 GRT

	rav := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           env.PayerAddr,
		ServiceProvider: env.ServiceProviderAddr,
		DataService:     env.SubstreamsDataService,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  valueAggregate,
		Metadata:        []byte{},
	}

	// Sign RAV with authorized signer key (not payer key)
	zlog.Debug("signing RAV with authorized signer key")
	signedRAV, err := horizon.Sign(domain, rav, signerKey)
	require.NoError(t, err)
	zlog.Debug("RAV signed successfully")

	// Verify signature locally first
	recoveredSigner, err := signedRAV.RecoverSigner(domain)
	require.NoError(t, err)
	require.Equal(t, signerAddr, recoveredSigner)
	zlog.Debug("signature verified locally", zap.Stringer("recovered_signer", recoveredSigner))

	// Call collect() via SubstreamsDataService
	dataServiceCut := uint64(100000) // 10% in PPM
	zlog.Info("calling SubstreamsDataService.collect() on chain", zap.String("data_service", env.SubstreamsDataService.Pretty()), zap.Uint64("chain_id", env.ChainID))
	tokensCollected, err := callDataServiceCollect(env.ctx, env.rpcURL, env.ServiceProviderKey, env.ChainID, env.SubstreamsDataService, env.ServiceProviderAddr, signedRAV, dataServiceCut, env)
	require.NoError(t, err)
	require.Equal(t, valueAggregate.Uint64(), tokensCollected)
	zlog.Info("SubstreamsDataService.collect() succeeded", zap.Uint64("tokens_collected", tokensCollected))

	// Verify tokensCollected mapping updated
	collected, err := env.CallTokensCollected(env.SubstreamsDataService, collectionID, env.ServiceProviderAddr, env.PayerAddr)
	require.NoError(t, err)
	require.Equal(t, valueAggregate.Uint64(), collected)

	t.Logf("Successfully collected %s tokens", valueAggregate.String())
}

// TestCollectRAVIncremental tests incremental RAV collection
func TestCollectRAVIncremental(t *testing.T) {
	env := SetupEnv(t)

	// Setup escrow and provision
	tokensToDeposit := new(big.Int)
	tokensToDeposit.SetString("10000000000000000000000", 10) // 10,000 GRT

	err := callMintGRT(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.GRTToken, env.PayerAddr, tokensToDeposit, env.ABIs.GRTToken)
	require.NoError(t, err, "Failed to mint GRT")

	err = callApproveGRT(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.GRTToken, env.PaymentsEscrow, tokensToDeposit, env.ABIs.GRTToken)
	require.NoError(t, err, "Failed to approve GRT")

	err = callDepositEscrow(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.PaymentsEscrow, env.CollectorAddress, env.ServiceProviderAddr, tokensToDeposit, env.ABIs.Escrow)
	require.NoError(t, err, "Failed to deposit to escrow")

	// Set up SubstreamsDataService: set provision tokens range (min = 0 for testing)
	err = callSetProvisionTokensRange(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.SubstreamsDataService, big.NewInt(0), env.ABIs.DataService)
	require.NoError(t, err, "Failed to set provision tokens range")

	provisionTokens := new(big.Int)
	provisionTokens.SetString("1000000000000000000000", 10) // 1,000 GRT
	maxVerifierCut := uint32(0)
	thawingPeriod := uint64(0)
	err = callSetProvision(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.Staking, env.ServiceProviderAddr, env.SubstreamsDataService, provisionTokens, maxVerifierCut, thawingPeriod, env.ABIs.Staking)
	require.NoError(t, err, "Failed to set provision")

	// Register service provider with SubstreamsDataService
	err = callRegisterWithDataService(env.ctx, env.rpcURL, env.ServiceProviderKey, env.ChainID, env.SubstreamsDataService, env.ServiceProviderAddr, env.ServiceProviderAddr, env.ABIs.DataService)
	require.NoError(t, err, "Failed to register with data service")

	// Create a dedicated signer key for signing RAVs
	// In the original contract, self-authorization is NOT supported - we must explicitly authorize
	signerKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	// Authorize the signer (payer authorizes the signer to sign RAVs on their behalf)
	err = callAuthorizeSigner(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.CollectorAddress, signerKey, env.ABIs.Collector)
	require.NoError(t, err, "Failed to authorize signer")

	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0xfedcba0987654321fedcba0987654321fedcba0987654321fedcba0987654321")[:])

	// First RAV: 1 GRT
	rav1 := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           env.PayerAddr,
		ServiceProvider: env.ServiceProviderAddr,
		DataService:     env.SubstreamsDataService,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  big.NewInt(1000000000000000000), // 1 GRT
		Metadata:        []byte{},
	}

	signedRAV1, err := horizon.Sign(domain, rav1, signerKey)
	require.NoError(t, err)

	dataServiceCut := uint64(100000) // 10% in PPM
	collected1, err := callDataServiceCollect(env.ctx, env.rpcURL, env.ServiceProviderKey, env.ChainID, env.SubstreamsDataService, env.ServiceProviderAddr, signedRAV1, dataServiceCut, env)
	require.NoError(t, err)
	require.Equal(t, uint64(1000000000000000000), collected1)

	// Second RAV: 3 GRT total (should collect 2 GRT delta)
	rav2 := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           env.PayerAddr,
		ServiceProvider: env.ServiceProviderAddr,
		DataService:     env.SubstreamsDataService,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  big.NewInt(3000000000000000000), // 3 GRT
		Metadata:        []byte{},
	}

	signedRAV2, err := horizon.Sign(domain, rav2, signerKey)
	require.NoError(t, err)

	collected2, err := callDataServiceCollect(env.ctx, env.rpcURL, env.ServiceProviderKey, env.ChainID, env.SubstreamsDataService, env.ServiceProviderAddr, signedRAV2, dataServiceCut, env)
	require.NoError(t, err)
	require.Equal(t, uint64(2000000000000000000), collected2) // Delta: 2 GRT

	// Verify total tokensCollected is 3 GRT
	totalCollected, err := env.CallTokensCollected(env.SubstreamsDataService, collectionID, env.ServiceProviderAddr, env.PayerAddr)
	require.NoError(t, err)
	require.Equal(t, uint64(3000000000000000000), totalCollected)

	t.Logf("Successfully collected incrementally: first=%d, second=%d, total=%d",
		collected1, collected2, totalCollected)
}

// ========== Contract Call Helpers ==========

// callMintGRT calls MockGRTToken.mint(address to, uint256 amount)
func callMintGRT(ctx testContext, rpcURL string, key *eth.PrivateKey, chainID uint64, token eth.Address, to eth.Address, amount *big.Int, abi *eth.ABI) error {
	mintFn := abi.FindFunctionByName("mint")
	if mintFn == nil {
		return fmt.Errorf("mint function not found in ABI")
	}

	data, err := mintFn.NewCall(to, amount).Encode()
	if err != nil {
		return fmt.Errorf("encoding mint call: %w", err)
	}

	return sendTransaction(ctx, rpcURL, key, chainID, &token, big.NewInt(0), data)
}

// callApproveGRT calls IERC20.approve(address spender, uint256 amount)
func callApproveGRT(ctx testContext, rpcURL string, key *eth.PrivateKey, chainID uint64, token eth.Address, spender eth.Address, amount *big.Int, abi *eth.ABI) error {
	approveFn := abi.FindFunctionByName("approve")
	if approveFn == nil {
		return fmt.Errorf("approve function not found in ABI")
	}

	data, err := approveFn.NewCall(spender, amount).Encode()
	if err != nil {
		return fmt.Errorf("encoding approve call: %w", err)
	}

	return sendTransaction(ctx, rpcURL, key, chainID, &token, big.NewInt(0), data)
}

// callDepositEscrow calls MockPaymentsEscrow.deposit(address collector, address receiver, uint256 amount)
func callDepositEscrow(ctx testContext, rpcURL string, key *eth.PrivateKey, chainID uint64, escrow eth.Address, collector eth.Address, receiver eth.Address, amount *big.Int, abi *eth.ABI) error {
	depositFn := abi.FindFunctionByName("deposit")
	if depositFn == nil {
		return fmt.Errorf("deposit function not found in ABI")
	}

	data, err := depositFn.NewCall(collector, receiver, amount).Encode()
	if err != nil {
		return fmt.Errorf("encoding deposit call: %w", err)
	}

	return sendTransaction(ctx, rpcURL, key, chainID, &escrow, big.NewInt(0), data)
}

// callSetProvision calls MockStaking.setProvision(address serviceProvider, address dataService, uint256 tokens, uint32 maxVerifierCut, uint64 thawingPeriod)
func callSetProvision(ctx testContext, rpcURL string, key *eth.PrivateKey, chainID uint64, staking eth.Address, serviceProvider eth.Address, dataService eth.Address, tokens *big.Int, maxVerifierCut uint32, thawingPeriod uint64, abi *eth.ABI) error {
	setProvisionFn := abi.FindFunctionByName("setProvision")
	if setProvisionFn == nil {
		return fmt.Errorf("setProvision function not found in ABI")
	}

	data, err := setProvisionFn.NewCall(serviceProvider, dataService, tokens, maxVerifierCut, thawingPeriod).Encode()
	if err != nil {
		return fmt.Errorf("encoding setProvision call: %w", err)
	}

	return sendTransaction(ctx, rpcURL, key, chainID, &staking, big.NewInt(0), data)
}

// callSetProvisionTokensRange calls SubstreamsDataService.setProvisionTokensRange(uint256 minimumProvisionTokens)
func callSetProvisionTokensRange(ctx testContext, rpcURL string, key *eth.PrivateKey, chainID uint64, dataService eth.Address, minimumProvisionTokens *big.Int, abi *eth.ABI) error {
	fn := abi.FindFunctionByName("setProvisionTokensRange")
	if fn == nil {
		return fmt.Errorf("setProvisionTokensRange function not found in ABI")
	}

	data, err := fn.NewCall(minimumProvisionTokens).Encode()
	if err != nil {
		return fmt.Errorf("encoding setProvisionTokensRange call: %w", err)
	}

	return sendTransaction(ctx, rpcURL, key, chainID, &dataService, big.NewInt(0), data)
}

// callRegisterWithDataService calls SubstreamsDataService.register(address indexer, bytes data)
// The data parameter is abi.encode(paymentsDestination)
func callRegisterWithDataService(ctx testContext, rpcURL string, key *eth.PrivateKey, chainID uint64, dataService eth.Address, indexer eth.Address, paymentsDestination eth.Address, abi *eth.ABI) error {
	fn := abi.FindFunctionByName("register")
	if fn == nil {
		return fmt.Errorf("register function not found in ABI")
	}

	// Encode the paymentsDestination as the data parameter (abi.encode(address))
	registerData := make([]byte, 32)
	copy(registerData[12:], paymentsDestination[:])

	data, err := fn.NewCall(indexer, registerData).Encode()
	if err != nil {
		return fmt.Errorf("encoding register call: %w", err)
	}

	return sendTransaction(ctx, rpcURL, key, chainID, &dataService, big.NewInt(0), data)
}

// callDataServiceCollect calls SubstreamsDataService.collect(address indexer, uint8 paymentType, bytes data)
// This is the recommended way to collect payments - through the data service contract
// Returns tokens collected (delta from previous collection)
func callDataServiceCollect(ctx testContext, rpcURL string, key *eth.PrivateKey, chainID uint64, dataService eth.Address, indexer eth.Address, signedRAV *horizon.SignedRAV, dataServiceCut uint64, env *TestEnv) (uint64, error) {
	rav := signedRAV.Message
	zlog.Debug("preparing SubstreamsDataService.collect() call",
		zap.Uint64("chain_id", chainID),
		zap.Stringer("data_service", dataService),
		zap.Stringer("indexer", indexer),
		zap.String("payer", rav.Payer.Pretty()),
		zap.String("service_provider", rav.ServiceProvider.Pretty()),
		zap.String("value_aggregate", rav.ValueAggregate.String()))

	// Query tokens collected before the call to calculate delta
	var collectedBefore uint64
	if env != nil {
		var err error
		collectedBefore, err = env.CallTokensCollected(rav.DataService, rav.CollectionID, rav.ServiceProvider, rav.Payer)
		if err != nil {
			return 0, fmt.Errorf("failed to query tokensCollected before: %w", err)
		}
		zlog.Debug("tokens collected before", zap.Uint64("amount", collectedBefore))
	}

	collectFn := env.ABIs.DataService.FindFunctionByName("collect")
	if collectFn == nil {
		return 0, fmt.Errorf("collect function not found in SubstreamsDataService ABI")
	}

	// Encode the data parameter for SubstreamsDataService: (SignedRAV, dataServiceCut)
	// Note: receiverDestination is read from contract state (paymentsDestination[indexer])
	encodedData := encodeDataServiceCollectData(signedRAV, dataServiceCut)

	// SubstreamsDataService.collect(address indexer, uint8 paymentType, bytes data)
	paymentType := uint8(0) // QueryFee payment type (enum value 0)
	calldata, err := collectFn.NewCall(indexer, paymentType, encodedData).Encode()
	if err != nil {
		return 0, fmt.Errorf("encoding SubstreamsDataService.collect call: %w", err)
	}

	// Send transaction
	zlog.Debug("sending SubstreamsDataService.collect() transaction", zap.Uint64("chain_id", chainID))
	if err := sendTransaction(ctx, rpcURL, key, chainID, &dataService, big.NewInt(0), calldata); err != nil {
		zlog.Error("SubstreamsDataService.collect() transaction failed", zap.Error(err), zap.Uint64("chain_id", chainID))
		return 0, err
	}

	// Query tokens collected after the call to calculate delta
	if env != nil {
		collectedAfter, err := env.CallTokensCollected(rav.DataService, rav.CollectionID, rav.ServiceProvider, rav.Payer)
		if err != nil {
			return 0, fmt.Errorf("failed to query tokensCollected after: %w", err)
		}
		delta := collectedAfter - collectedBefore
		zlog.Debug("SubstreamsDataService.collect() transaction confirmed", zap.Uint64("tokens_collected_delta", delta), zap.Uint64("total_collected", collectedAfter))
		return delta, nil
	}

	// Fallback: return the value aggregate (for tests that don't pass env)
	zlog.Debug("SubstreamsDataService.collect() transaction confirmed", zap.Uint64("tokens_collected", rav.ValueAggregate.Uint64()))
	return rav.ValueAggregate.Uint64(), nil
}

// callCollect calls GraphTallyCollector.collect(uint8 paymentType, bytes data)
// DEPRECATED: Use callDataServiceCollect instead to go through SubstreamsDataService
// Returns tokens collected (delta from previous collection)
func callCollect(ctx testContext, rpcURL string, key *eth.PrivateKey, chainID uint64, collector eth.Address, signedRAV *horizon.SignedRAV, dataServiceCut uint64, receiverDestination eth.Address, env *TestEnv) (uint64, error) {
	rav := signedRAV.Message
	zlog.Debug("preparing collect() call",
		zap.Uint64("chain_id", chainID),
		zap.Stringer("collector", collector),
		zap.String("payer", rav.Payer.Pretty()),
		zap.String("service_provider", rav.ServiceProvider.Pretty()),
		zap.String("value_aggregate", rav.ValueAggregate.String()))

	// Query tokens collected before the call to calculate delta
	var collectedBefore uint64
	if env != nil {
		var err error
		collectedBefore, err = env.CallTokensCollected(rav.DataService, rav.CollectionID, rav.ServiceProvider, rav.Payer)
		if err != nil {
			return 0, fmt.Errorf("failed to query tokensCollected before: %w", err)
		}
		zlog.Debug("tokens collected before", zap.Uint64("amount", collectedBefore))
	}

	collectFn := env.ABIs.Collector.FindFunctionByName("collect")
	if collectFn == nil {
		return 0, fmt.Errorf("collect function not found in ABI")
	}

	// Encode the data parameter: (SignedRAV, dataServiceCut, receiverDestination)
	encodedData := encodeCollectData(signedRAV, dataServiceCut, receiverDestination)

	// Use eth-go to encode the outer collect(uint8, bytes, uint256) call
	// The ABI has two overloads: collect(uint8, bytes) and collect(uint8, bytes, uint256)
	// FindFunctionByName returns the 3-parameter version, so we pass tokensToCollect = 0 (collect all)
	paymentType := uint8(0)          // QueryFee payment type (enum value 0)
	tokensToCollect := big.NewInt(0) // 0 means collect all available
	calldata, err := collectFn.NewCall(paymentType, encodedData, tokensToCollect).Encode()
	if err != nil {
		return 0, fmt.Errorf("encoding collect call: %w", err)
	}

	// Send transaction
	zlog.Debug("sending collect() transaction", zap.Uint64("chain_id", chainID))
	if err := sendTransaction(ctx, rpcURL, key, chainID, &collector, big.NewInt(0), calldata); err != nil {
		zlog.Error("collect() transaction failed", zap.Error(err), zap.Uint64("chain_id", chainID))
		return 0, err
	}

	// Query tokens collected after the call to calculate delta
	if env != nil {
		collectedAfter, err := env.CallTokensCollected(rav.DataService, rav.CollectionID, rav.ServiceProvider, rav.Payer)
		if err != nil {
			return 0, fmt.Errorf("failed to query tokensCollected after: %w", err)
		}
		delta := collectedAfter - collectedBefore
		zlog.Debug("collect() transaction confirmed", zap.Uint64("tokens_collected_delta", delta), zap.Uint64("total_collected", collectedAfter))
		return delta, nil
	}

	// Fallback: return the value aggregate (for tests that don't pass env)
	zlog.Debug("collect() transaction confirmed", zap.Uint64("tokens_collected", rav.ValueAggregate.Uint64()))
	return rav.ValueAggregate.Uint64(), nil
}

// collectDataEncoderABI is a synthetic ABI used to encode the collect() data parameter.
// The data parameter is ABI-encoded (SignedRAV, uint256, address) which the contract
// decodes internally. We use this synthetic ABI to leverage eth-go's tuple encoding.
var collectDataEncoderABI *eth.ABI

func init() {
	var err error
	// Create a synthetic ABI with a function that has the same parameter types as the collect data
	// The function name doesn't matter - we only use it to encode the arguments
	collectDataEncoderABI, err = eth.ParseABIFromBytes([]byte(`{
		"abi": [{
			"type": "function",
			"name": "encode",
			"inputs": [
				{
					"name": "signedRAV",
					"type": "tuple",
					"components": [
						{
							"name": "rav",
							"type": "tuple",
							"components": [
								{"name": "collectionId", "type": "bytes32"},
								{"name": "payer", "type": "address"},
								{"name": "serviceProvider", "type": "address"},
								{"name": "dataService", "type": "address"},
								{"name": "timestampNs", "type": "uint64"},
								{"name": "valueAggregate", "type": "uint128"},
								{"name": "metadata", "type": "bytes"}
							]
						},
						{"name": "signature", "type": "bytes"}
					]
				},
				{"name": "dataServiceCut", "type": "uint256"},
				{"name": "receiverDestination", "type": "address"}
			]
		}]
	}`))
	if err != nil {
		panic(fmt.Sprintf("failed to parse collectDataEncoderABI: %v", err))
	}

	// Synthetic ABI for SubstreamsDataService.collect() data parameter
	// Data is (SignedRAV, dataServiceCut) - no receiverDestination as it's read from contract state
	dataServiceCollectEncoderABI, err = eth.ParseABIFromBytes([]byte(`{
		"abi": [{
			"type": "function",
			"name": "encode",
			"inputs": [
				{
					"name": "signedRAV",
					"type": "tuple",
					"components": [
						{
							"name": "rav",
							"type": "tuple",
							"components": [
								{"name": "collectionId", "type": "bytes32"},
								{"name": "payer", "type": "address"},
								{"name": "serviceProvider", "type": "address"},
								{"name": "dataService", "type": "address"},
								{"name": "timestampNs", "type": "uint64"},
								{"name": "valueAggregate", "type": "uint128"},
								{"name": "metadata", "type": "bytes"}
							]
						},
						{"name": "signature", "type": "bytes"}
					]
				},
				{"name": "dataServiceCut", "type": "uint256"}
			]
		}]
	}`))
	if err != nil {
		panic(fmt.Sprintf("failed to parse dataServiceCollectEncoderABI: %v", err))
	}
}

// dataServiceCollectEncoderABI is a synthetic ABI for encoding SubstreamsDataService.collect() data parameter
var dataServiceCollectEncoderABI *eth.ABI

// encodeDataServiceCollectData encodes (SignedRAV, uint256 dataServiceCut) for SubstreamsDataService.collect()
// Unlike encodeCollectData, this does not include receiverDestination as SubstreamsDataService
// reads it from its paymentsDestination mapping
func encodeDataServiceCollectData(signedRAV *horizon.SignedRAV, dataServiceCut uint64) []byte {
	encodeFn := dataServiceCollectEncoderABI.FindFunctionByName("encode")
	if encodeFn == nil {
		panic("encode function not found in dataServiceCollectEncoderABI")
	}

	rav := signedRAV.Message

	// Build the nested SignedRAV tuple using maps for eth-go's tuple encoding
	ravTuple := map[string]interface{}{
		"collectionId":    rav.CollectionID[:],
		"payer":           rav.Payer,
		"serviceProvider": rav.ServiceProvider,
		"dataService":     rav.DataService,
		"timestampNs":     rav.TimestampNs,
		"valueAggregate":  rav.ValueAggregate,
		"metadata":        rav.Metadata,
	}

	// Convert signature from V+R+S (eth-go format) to R+S+V (Solidity ECDSA format)
	sig := signedRAV.Signature
	rsv := make([]byte, 65)
	copy(rsv[0:32], sig[1:33])   // R (32 bytes)
	copy(rsv[32:64], sig[33:65]) // S (32 bytes)
	rsv[64] = sig[0]             // V (1 byte)

	signedRAVTuple := map[string]interface{}{
		"rav":       ravTuple,
		"signature": rsv,
	}

	// Encode the call and strip the 4-byte function selector to get raw tuple encoding
	data, err := encodeFn.NewCall(signedRAVTuple, big.NewInt(int64(dataServiceCut))).Encode()
	if err != nil {
		panic(fmt.Sprintf("encoding SubstreamsDataService collect data: %v", err))
	}

	// Return just the encoded arguments (strip 4-byte selector)
	return data[4:]
}

// encodeCollectData encodes (SignedRAV, uint256 dataServiceCut, address receiverDestination) for collect()
// This is the inner bytes parameter for collect(). It encodes a complex nested struct
// that the contract decodes internally.
func encodeCollectData(signedRAV *horizon.SignedRAV, dataServiceCut uint64, receiverDestination eth.Address) []byte {
	encodeFn := collectDataEncoderABI.FindFunctionByName("encode")
	if encodeFn == nil {
		panic("encode function not found in collectDataEncoderABI")
	}

	rav := signedRAV.Message

	// Build the nested SignedRAV tuple using maps for eth-go's tuple encoding
	ravTuple := map[string]interface{}{
		"collectionId":    rav.CollectionID[:],
		"payer":           rav.Payer,
		"serviceProvider": rav.ServiceProvider,
		"dataService":     rav.DataService,
		"timestampNs":     rav.TimestampNs,
		"valueAggregate":  rav.ValueAggregate,
		"metadata":        rav.Metadata,
	}

	// Convert signature from V+R+S (eth-go format) to R+S+V (Solidity ECDSA format)
	sig := signedRAV.Signature
	rsv := make([]byte, 65)
	copy(rsv[0:32], sig[1:33])   // R (32 bytes)
	copy(rsv[32:64], sig[33:65]) // S (32 bytes)
	rsv[64] = sig[0]             // V (1 byte)

	signedRAVTuple := map[string]interface{}{
		"rav":       ravTuple,
		"signature": rsv,
	}

	// Encode the call and strip the 4-byte function selector to get raw tuple encoding
	data, err := encodeFn.NewCall(signedRAVTuple, big.NewInt(int64(dataServiceCut)), receiverDestination).Encode()
	if err != nil {
		panic(fmt.Sprintf("encoding collect data: %v", err))
	}

	// Debug: print the encoded data for comparison with recoverRAVSigner
	fmt.Printf("\n=== encodeCollectData Debug ===\n")
	fmt.Printf("Total encoded length (with selector): %d bytes\n", len(data))
	fmt.Printf("Data parameter length (without selector): %d bytes\n", len(data)-4)
	fmt.Printf("Signature (R+S+V): %x\n", rsv)

	// Return just the encoded arguments (strip 4-byte selector)
	return data[4:]
}

// CallTokensCollected queries tokensCollected mapping
func (env *TestEnv) CallTokensCollected(dataService eth.Address, collectionID horizon.CollectionID, receiver eth.Address, payer eth.Address) (uint64, error) {
	tokensCollectedFn := env.ABIs.Collector.FindFunctionByName("tokensCollected")
	if tokensCollectedFn == nil {
		return 0, fmt.Errorf("tokensCollected function not found in ABI")
	}

	// eth-go expects []byte for bytes32 parameters
	data, err := tokensCollectedFn.NewCall(dataService, collectionID[:], receiver, payer).Encode()
	if err != nil {
		return 0, fmt.Errorf("encoding tokensCollected call: %w", err)
	}

	result, err := env.CallContract(env.CollectorAddress, data)
	if err != nil {
		return 0, err
	}

	// Result is uint256 (32 bytes)
	return binary.BigEndian.Uint64(result[24:32]), nil
}
