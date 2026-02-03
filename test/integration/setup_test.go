package integration

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/streamingfast/eth-go"
	"github.com/streamingfast/eth-go/rpc"
	"github.com/streamingfast/logging"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/graphprotocol/substreams-data-service/horizon"
	"github.com/graphprotocol/substreams-data-service/horizon/devenv"
)

var zlog, _ = logging.PackageLogger("integration_tests", "github.com/graphprotocol/substreams-data-service/test/integration")

func init() {
	logging.InstantiateLoggers()
}

// TestEnv wraps devenv.Env for backwards compatibility with existing tests
type TestEnv = devenv.Env

// Account wraps devenv.Account for backwards compatibility
type Account = devenv.Account

// Contract wraps devenv.Contract for backwards compatibility
type Contract = devenv.Contract

// TestSetupConfig wraps devenv.TestSetupConfig for backwards compatibility
type TestSetupConfig = devenv.TestSetupConfig

// TestSetupResult wraps devenv.TestSetupResult for backwards compatibility
type TestSetupResult = devenv.TestSetupResult

// SetupEnv returns the shared test environment (uses devenv singleton)
func SetupEnv(t *testing.T) *TestEnv {
	t.Helper()
	env := devenv.Get()
	require.NotNil(t, env, "Development environment not started - ensure TestMain calls devenv.Start()")
	return env
}

// DefaultTestSetupConfig returns the default test setup configuration
func DefaultTestSetupConfig() *TestSetupConfig {
	return devenv.DefaultTestSetupConfig()
}

// SetupTestWithSigner performs common test setup: fund escrow, set provision, register, and authorize signer
func SetupTestWithSigner(t *testing.T, env *TestEnv, config *TestSetupConfig) *TestSetupResult {
	t.Helper()
	result, err := env.SetupTestWithSigner(config)
	require.NoError(t, err, "Failed to setup test with signer")
	return result
}

// mustNewCollectionID creates a CollectionID from a hex string or panics
func mustNewCollectionID(hexStr string) horizon.CollectionID {
	return devenv.MustNewCollectionID(hexStr)
}

// ========== Contract Call Helpers (delegates to devenv) ==========

// callMintGRT mints GRT tokens to an address
func callMintGRT(env *TestEnv, to eth.Address, amount *big.Int) error {
	return env.MintGRT(to, amount)
}

// callApproveGRT approves the escrow contract to spend GRT
func callApproveGRT(env *TestEnv, amount *big.Int) error {
	return env.ApproveGRT(amount)
}

// callDepositEscrow deposits GRT into escrow
func callDepositEscrow(env *TestEnv, amount *big.Int) error {
	return env.DepositEscrow(amount)
}

// callSetProvision sets provision tokens for service provider
func callSetProvision(env *TestEnv, tokens *big.Int, maxVerifierCut uint32, thawingPeriod uint64) error {
	return env.SetProvision(tokens, maxVerifierCut, thawingPeriod)
}

// callSetProvisionTokensRange sets the minimum provision tokens for the data service
func callSetProvisionTokensRange(env *TestEnv, minimumProvisionTokens *big.Int) error {
	return env.SetProvisionTokensRange(minimumProvisionTokens)
}

// callRegisterWithDataService registers the service provider with the data service
func callRegisterWithDataService(env *TestEnv) error {
	return env.RegisterServiceProvider()
}

// callAuthorizeSigner authorizes a signer key to sign RAVs for the payer
func callAuthorizeSigner(env *TestEnv, signerKey *eth.PrivateKey) error {
	return env.AuthorizeSigner(signerKey)
}

// callThawSigner initiates thawing for a signer
func callThawSigner(env *TestEnv, signer eth.Address) error {
	return env.ThawSigner(signer)
}

// callRevokeAuthorizedSigner revokes a signer after thawing
func callRevokeAuthorizedSigner(env *TestEnv, signer eth.Address) error {
	return env.RevokeAuthorizedSigner(signer)
}

// callRevokeSigner performs the two-step revoke flow: thaw + revoke
func callRevokeSigner(env *TestEnv, signer eth.Address) error {
	return env.RevokeSigner(signer)
}

// sendTransaction is exposed for backwards compatibility
func sendTransaction(ctx context.Context, rpcClient *rpc.Client, key *eth.PrivateKey, chainID uint64, to *eth.Address, value *big.Int, data []byte) error {
	return devenv.SendTransaction(ctx, rpcClient, key, chainID, to, value, data)
}

// ========== RAV/Collection Helpers ==========

// callTokensCollected queries tokensCollected mapping
func callTokensCollected(env *TestEnv, dataService eth.Address, collectionID horizon.CollectionID, receiver eth.Address, payer eth.Address) (uint64, error) {
	// eth-go expects []byte for bytes32 parameters
	data, err := env.Collector.CallData("tokensCollected", dataService, collectionID[:], receiver, payer)
	if err != nil {
		return 0, fmt.Errorf("encoding tokensCollected call: %w", err)
	}

	result, err := env.CallContract(env.Collector.Address, data)
	if err != nil {
		return 0, err
	}

	// Result is uint256 (32 bytes)
	return binary.BigEndian.Uint64(result[24:32]), nil
}

// callEncodeRAV calls encodeRAV to get the EIP-712 hash
func callEncodeRAV(env *TestEnv, rav *horizon.RAV) (eth.Hash, error) {
	encodeRAVFn := env.Collector.ABI.FindFunctionByName("encodeRAV")
	if encodeRAVFn == nil {
		return nil, fmt.Errorf("encodeRAV function not found in ABI")
	}

	ravTuple := map[string]interface{}{
		"collectionId":    rav.CollectionID[:],
		"payer":           rav.Payer,
		"serviceProvider": rav.ServiceProvider,
		"dataService":     rav.DataService,
		"timestampNs":     rav.TimestampNs,
		"valueAggregate":  rav.ValueAggregate,
		"metadata":        rav.Metadata,
	}

	data, err := encodeRAVFn.NewCall(ravTuple).Encode()
	if err != nil {
		return nil, fmt.Errorf("encoding encodeRAV call: %w", err)
	}

	result, err := env.CallContract(env.Collector.Address, data)
	if err != nil {
		return nil, err
	}

	return eth.Hash(result), nil
}

// callRecoverRAVSigner calls recoverRAVSigner to recover the signer address
func callRecoverRAVSigner(env *TestEnv, signedRAV *horizon.SignedRAV) (eth.Address, error) {
	recoverRAVSignerFn := env.Collector.ABI.FindFunctionByName("recoverRAVSigner")
	if recoverRAVSignerFn == nil {
		return nil, fmt.Errorf("recoverRAVSigner function not found in ABI")
	}

	rav := signedRAV.Message

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

	data, err := recoverRAVSignerFn.NewCall(signedRAVTuple).Encode()
	if err != nil {
		return nil, fmt.Errorf("encoding recoverRAVSigner call: %w", err)
	}

	result, err := env.CallContract(env.Collector.Address, data)
	if err != nil {
		return nil, err
	}

	if len(result) >= 32 {
		return eth.Address(result[12:32]), nil
	}
	return nil, fmt.Errorf("unexpected result length: %d", len(result))
}

// ========== Data Service Collect Helpers ==========

// collectDataEncoderABI is a synthetic ABI used to encode the collect() data parameter.
var collectDataEncoderABI *eth.ABI
var dataServiceCollectEncoderABI *eth.ABI

func init() {
	var err error
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

// encodeDataServiceCollectData encodes (SignedRAV, uint256 dataServiceCut) for SubstreamsDataService.collect()
func encodeDataServiceCollectData(signedRAV *horizon.SignedRAV, dataServiceCut uint64) []byte {
	encodeFn := dataServiceCollectEncoderABI.FindFunctionByName("encode")
	if encodeFn == nil {
		panic("encode function not found in dataServiceCollectEncoderABI")
	}

	rav := signedRAV.Message

	ravTuple := map[string]interface{}{
		"collectionId":    rav.CollectionID[:],
		"payer":           rav.Payer,
		"serviceProvider": rav.ServiceProvider,
		"dataService":     rav.DataService,
		"timestampNs":     rav.TimestampNs,
		"valueAggregate":  rav.ValueAggregate,
		"metadata":        rav.Metadata,
	}

	sig := signedRAV.Signature
	rsv := make([]byte, 65)
	copy(rsv[0:32], sig[1:33])   // R (32 bytes)
	copy(rsv[32:64], sig[33:65]) // S (32 bytes)
	rsv[64] = sig[0]             // V (1 byte)

	signedRAVTuple := map[string]interface{}{
		"rav":       ravTuple,
		"signature": rsv,
	}

	data, err := encodeFn.NewCall(signedRAVTuple, big.NewInt(int64(dataServiceCut))).Encode()
	if err != nil {
		panic(fmt.Sprintf("encoding SubstreamsDataService collect data: %v", err))
	}

	return data[4:]
}

// encodeCollectData encodes (SignedRAV, uint256 dataServiceCut, address receiverDestination) for collect()
func encodeCollectData(signedRAV *horizon.SignedRAV, dataServiceCut uint64, receiverDestination eth.Address) []byte {
	encodeFn := collectDataEncoderABI.FindFunctionByName("encode")
	if encodeFn == nil {
		panic("encode function not found in collectDataEncoderABI")
	}

	rav := signedRAV.Message

	ravTuple := map[string]interface{}{
		"collectionId":    rav.CollectionID[:],
		"payer":           rav.Payer,
		"serviceProvider": rav.ServiceProvider,
		"dataService":     rav.DataService,
		"timestampNs":     rav.TimestampNs,
		"valueAggregate":  rav.ValueAggregate,
		"metadata":        rav.Metadata,
	}

	sig := signedRAV.Signature
	rsv := make([]byte, 65)
	copy(rsv[0:32], sig[1:33])
	copy(rsv[32:64], sig[33:65])
	rsv[64] = sig[0]

	signedRAVTuple := map[string]interface{}{
		"rav":       ravTuple,
		"signature": rsv,
	}

	data, err := encodeFn.NewCall(signedRAVTuple, big.NewInt(int64(dataServiceCut)), receiverDestination).Encode()
	if err != nil {
		panic(fmt.Sprintf("encoding collect data: %v", err))
	}

	fmt.Printf("\n=== encodeCollectData Debug ===\n")
	fmt.Printf("Total encoded length (with selector): %d bytes\n", len(data))
	fmt.Printf("Data parameter length (without selector): %d bytes\n", len(data)-4)
	fmt.Printf("Signature (R+S+V): %x\n", rsv)

	return data[4:]
}

// callDataServiceCollect calls SubstreamsDataService.collect()
func callDataServiceCollect(env *TestEnv, signedRAV *horizon.SignedRAV, dataServiceCut uint64) (uint64, error) {
	rav := signedRAV.Message
	zlog.Debug("preparing SubstreamsDataService.collect() call",
		zap.Uint64("chain_id", env.ChainID),
		zap.Stringer("data_service", env.DataService.Address),
		zap.Stringer("indexer", env.ServiceProvider.Address),
		zap.String("payer", rav.Payer.Pretty()),
		zap.String("service_provider", rav.ServiceProvider.Pretty()),
		zap.String("value_aggregate", rav.ValueAggregate.String()))

	// Query tokens collected before the call to calculate delta
	collectedBefore, err := callTokensCollected(env, rav.DataService, rav.CollectionID, rav.ServiceProvider, rav.Payer)
	if err != nil {
		return 0, fmt.Errorf("failed to query tokensCollected before: %w", err)
	}
	zlog.Debug("tokens collected before", zap.Uint64("amount", collectedBefore))

	encodedData := encodeDataServiceCollectData(signedRAV, dataServiceCut)

	paymentType := uint8(0) // QueryFee payment type
	calldata, err := env.DataService.CallData("collect", env.ServiceProvider.Address, paymentType, encodedData)
	if err != nil {
		return 0, fmt.Errorf("encoding SubstreamsDataService.collect call: %w", err)
	}

	zlog.Debug("sending SubstreamsDataService.collect() transaction", zap.Uint64("chain_id", env.ChainID))
	if err := devenv.SendTransaction(context.Background(), getRPCClient(env), env.ServiceProvider.PrivateKey, env.ChainID, &env.DataService.Address, big.NewInt(0), calldata); err != nil {
		zlog.Error("SubstreamsDataService.collect() transaction failed", zap.Error(err), zap.Uint64("chain_id", env.ChainID))
		return 0, err
	}

	// Query tokens collected after the call to calculate delta
	collectedAfter, err := callTokensCollected(env, rav.DataService, rav.CollectionID, rav.ServiceProvider, rav.Payer)
	if err != nil {
		return 0, fmt.Errorf("failed to query tokensCollected after: %w", err)
	}
	delta := collectedAfter - collectedBefore
	zlog.Debug("SubstreamsDataService.collect() transaction confirmed", zap.Uint64("tokens_collected_delta", delta), zap.Uint64("total_collected", collectedAfter))
	return delta, nil
}

// getRPCClient returns the RPC client from the environment (used internally)
func getRPCClient(env *TestEnv) *rpc.Client {
	return rpc.NewClient(env.RPCURL)
}

// Helper for CallContract that returns hex string
func callContractHex(env *TestEnv, to eth.Address, data []byte) (string, error) {
	result, err := env.CallContract(to, data)
	if err != nil {
		return "", err
	}
	return "0x" + hex.EncodeToString(result), nil
}

// Helper for finding CallContract result as string
func callContractString(rpcClient *rpc.Client, ctx context.Context, to eth.Address, data []byte) (string, error) {
	params := rpc.CallParams{
		To:   to,
		Data: data,
	}

	resultHex, err := rpcClient.Call(ctx, params)
	if err != nil {
		return "", err
	}

	if strings.HasPrefix(resultHex, "0x") {
		resultHex = resultHex[2:]
	}

	return resultHex, nil
}
