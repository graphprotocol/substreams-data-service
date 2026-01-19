package integration

import (
	"context"
	"encoding/binary"
	"encoding/hex"
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

	// Setup: Set provision for service provider in staking contract
	provisionTokens := new(big.Int)
	provisionTokens.SetString("1000000000000000000000", 10) // 1,000 GRT
	maxVerifierCut := uint32(0)
	thawingPeriod := uint64(0)
	zlog.Debug("setting provision", zap.String("service_provider", env.ServiceProviderAddr.Pretty()), zap.String("amount", provisionTokens.String()))
	err = callSetProvision(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.Staking, env.ServiceProviderAddr, env.DataServiceAddr, provisionTokens, maxVerifierCut, thawingPeriod, env.ABIs.Staking)
	require.NoError(t, err, "Failed to set provision")

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
		DataService:     env.DataServiceAddr,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  valueAggregate,
		Metadata:        []byte{},
	}

	// Sign RAV with payer key
	zlog.Debug("signing RAV with payer key")
	signedRAV, err := horizon.Sign(domain, rav, env.PayerKey)
	require.NoError(t, err)
	zlog.Debug("RAV signed successfully")

	// Verify signature locally first
	recoveredSigner, err := signedRAV.RecoverSigner(domain)
	require.NoError(t, err)
	require.Equal(t, env.PayerAddr, recoveredSigner)
	zlog.Debug("signature verified locally", zap.Stringer("recovered_signer", recoveredSigner))

	// Call collect() from data service account
	dataServiceCut := uint64(100000) // 10% in PPM
	zlog.Info("calling collect() on chain", zap.String("collector", env.CollectorAddress.Pretty()), zap.Uint64("chain_id", env.ChainID))
	tokensCollected, err := callCollect(env.ctx, env.rpcURL, env.DataServiceKey, env.ChainID, env.CollectorAddress, signedRAV, dataServiceCut, eth.Address{}, env)
	require.NoError(t, err)
	require.Equal(t, valueAggregate.Uint64(), tokensCollected)
	zlog.Info("collect() succeeded", zap.Uint64("tokens_collected", tokensCollected))

	// Verify tokensCollected mapping updated
	collected, err := env.CallTokensCollected(env.DataServiceAddr, collectionID, env.ServiceProviderAddr, env.PayerAddr)
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

	provisionTokens := new(big.Int)
	provisionTokens.SetString("1000000000000000000000", 10) // 1,000 GRT
	maxVerifierCut := uint32(0)
	thawingPeriod := uint64(0)
	err = callSetProvision(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.Staking, env.ServiceProviderAddr, env.DataServiceAddr, provisionTokens, maxVerifierCut, thawingPeriod, env.ABIs.Staking)
	require.NoError(t, err, "Failed to set provision")

	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0xfedcba0987654321fedcba0987654321fedcba0987654321fedcba0987654321")[:])

	// First RAV: 1 GRT
	rav1 := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           env.PayerAddr,
		ServiceProvider: env.ServiceProviderAddr,
		DataService:     env.DataServiceAddr,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  big.NewInt(1000000000000000000), // 1 GRT
		Metadata:        []byte{},
	}

	signedRAV1, err := horizon.Sign(domain, rav1, env.PayerKey)
	require.NoError(t, err)

	dataServiceCut := uint64(100000) // 10% in PPM
	collected1, err := callCollect(env.ctx, env.rpcURL, env.DataServiceKey, env.ChainID, env.CollectorAddress, signedRAV1, dataServiceCut, eth.Address{}, env)
	require.NoError(t, err)
	require.Equal(t, uint64(1000000000000000000), collected1)

	// Second RAV: 3 GRT total (should collect 2 GRT delta)
	rav2 := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           env.PayerAddr,
		ServiceProvider: env.ServiceProviderAddr,
		DataService:     env.DataServiceAddr,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  big.NewInt(3000000000000000000), // 3 GRT
		Metadata:        []byte{},
	}

	signedRAV2, err := horizon.Sign(domain, rav2, env.PayerKey)
	require.NoError(t, err)

	collected2, err := callCollect(env.ctx, env.rpcURL, env.DataServiceKey, env.ChainID, env.CollectorAddress, signedRAV2, dataServiceCut, eth.Address{}, env)
	require.NoError(t, err)
	require.Equal(t, uint64(2000000000000000000), collected2) // Delta: 2 GRT

	// Verify total tokensCollected is 3 GRT
	totalCollected, err := env.CallTokensCollected(env.DataServiceAddr, collectionID, env.ServiceProviderAddr, env.PayerAddr)
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

// callCollect calls GraphTallyCollectorFull.collect(uint8 paymentType, bytes data)
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

	// Function selector: collect(uint8,bytes) = 0x7f07d283
	selector, _ := hex.DecodeString("7f07d283")

	// Encode the data parameter: (SignedRAV, dataServiceCut, receiverDestination)
	encodedData := encodeCollectData(signedRAV, dataServiceCut, receiverDestination)

	// Build full calldata: selector + paymentType + offset to bytes + bytes data
	paymentType := uint8(1) // TAP payment type

	calldata := make([]byte, 0, 1024)
	calldata = append(calldata, selector...)

	// paymentType (uint8 padded to 32 bytes)
	paymentTypeBytes := make([]byte, 32)
	paymentTypeBytes[31] = paymentType
	calldata = append(calldata, paymentTypeBytes...)

	// offset to bytes (points to 64 = 0x40)
	offsetBytes := make([]byte, 32)
	offsetBytes[31] = 0x40
	calldata = append(calldata, offsetBytes...)

	// bytes length
	lengthBytes := make([]byte, 32)
	binary.BigEndian.PutUint64(lengthBytes[24:], uint64(len(encodedData)))
	calldata = append(calldata, lengthBytes...)

	// bytes data (padded to 32-byte boundary)
	paddedData := make([]byte, ((len(encodedData)+31)/32)*32)
	copy(paddedData, encodedData)
	calldata = append(calldata, paddedData...)

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

// encodeCollectData encodes (SignedRAV, uint256 dataServiceCut, address receiverDestination) for collect()
func encodeCollectData(signedRAV *horizon.SignedRAV, dataServiceCut uint64, receiverDestination eth.Address) []byte {
	rav := signedRAV.Message

	// This is ABI encoding: (SignedRAV tuple, uint256, address)
	buf := make([]byte, 0, 2048)

	// Offset to SignedRAV tuple (0x60 = 96)
	buf = append(buf, padLeft(big.NewInt(96).Bytes(), 32)...)

	// dataServiceCut (uint256)
	buf = append(buf, padLeft(big.NewInt(int64(dataServiceCut)).Bytes(), 32)...)

	// receiverDestination (address)
	buf = append(buf, padLeft(receiverDestination[:], 32)...)

	// Now encode SignedRAV tuple
	// SignedRAV has: rav (tuple with dynamic bytes) and signature (bytes)
	// Offset to rav (0x40 = 64)
	buf = append(buf, padLeft(big.NewInt(64).Bytes(), 32)...)

	// Offset to signature (calculated from the end of RAV content)
	// RAV content: 7 fixed slots (224 bytes) + metadata length slot (32 bytes) + padded metadata data
	ravContentSize := int64(7*32 + 32) // 7 fixed slots + metadata length slot
	if len(rav.Metadata) > 0 {
		ravContentSize += int64(((len(rav.Metadata) + 31) / 32) * 32)
	}
	sigOffset := 64 + ravContentSize // 64 = offset to rav (32) + offset to sig (32)
	buf = append(buf, padLeft(big.NewInt(sigOffset).Bytes(), 32)...)

	// Encode RAV tuple
	buf = append(buf, rav.CollectionID[:]...)
	buf = append(buf, padLeft(rav.Payer[:], 32)...)
	buf = append(buf, padLeft(rav.ServiceProvider[:], 32)...)
	buf = append(buf, padLeft(rav.DataService[:], 32)...)
	timestampBytes := make([]byte, 32)
	binary.BigEndian.PutUint64(timestampBytes[24:], rav.TimestampNs)
	buf = append(buf, timestampBytes...)
	buf = append(buf, padLeft(rav.ValueAggregate.Bytes(), 32)...)
	buf = append(buf, padLeft(big.NewInt(7*32).Bytes(), 32)...) // offset to metadata
	buf = append(buf, padLeft(big.NewInt(int64(len(rav.Metadata))).Bytes(), 32)...)
	if len(rav.Metadata) > 0 {
		paddedMetadata := make([]byte, ((len(rav.Metadata)+31)/32)*32)
		copy(paddedMetadata, rav.Metadata)
		buf = append(buf, paddedMetadata...)
	}

	// Encode signature
	// eth.Signature is V+R+S (65 bytes) but Solidity ECDSA.recover expects R+S+V
	buf = append(buf, padLeft(big.NewInt(int64(len(signedRAV.Signature))).Bytes(), 32)...)
	paddedSig := make([]byte, ((len(signedRAV.Signature)+31)/32)*32)
	sig := signedRAV.Signature
	copy(paddedSig[0:32], sig[1:33])   // R (32 bytes)
	copy(paddedSig[32:64], sig[33:65]) // S (32 bytes)
	paddedSig[64] = sig[0]             // V (1 byte)
	buf = append(buf, paddedSig...)

	return buf
}

// CallTokensCollected queries tokensCollected mapping
func (env *TestEnv) CallTokensCollected(dataService eth.Address, collectionID horizon.CollectionID, receiver eth.Address, payer eth.Address) (uint64, error) {
	// Function selector: tokensCollected(address,bytes32,address,address) = 0x181250ff
	selector, _ := hex.DecodeString("181250ff")

	data := make([]byte, 4+32+32+32+32)
	copy(data[0:4], selector)
	copy(data[4+12:36], dataService[:])
	copy(data[36:68], collectionID[:])
	copy(data[68+12:100], receiver[:])
	copy(data[100+12:132], payer[:])

	result, err := env.CallContract(env.CollectorAddress, data)
	if err != nil {
		return 0, err
	}

	// Result is uint256 (32 bytes)
	return binary.BigEndian.Uint64(result[24:32]), nil
}
