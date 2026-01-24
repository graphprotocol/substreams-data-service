package integration

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/streamingfast/eth-go"
	horizon "github.com/streamingfast/horizon-go"
	"github.com/stretchr/testify/require"
)

// CallEncodeRAV calls encodeRAV(ReceiptAggregateVoucher calldata rav) which returns the EIP-712 hash
func (env *TestEnv) CallEncodeRAV(rav *horizon.RAV) (eth.Hash, error) {
	encodeRAVFn := env.Collector.ABI.FindFunctionByName("encodeRAV")
	if encodeRAVFn == nil {
		return nil, fmt.Errorf("encodeRAV function not found in ABI")
	}

	// Build the RAV tuple using a map for eth-go's tuple encoding
	// eth-go expects []byte for bytes32 and []byte for bytes types
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

	// eth.Hash is []byte, so just return the result directly
	return eth.Hash(result), nil
}

// CallRecoverRAVSigner calls recoverRAVSigner(SignedRAV calldata signedRAV) which returns the signer address
func (env *TestEnv) CallRecoverRAVSigner(signedRAV *horizon.SignedRAV) (eth.Address, error) {
	recoverRAVSignerFn := env.Collector.ABI.FindFunctionByName("recoverRAVSigner")
	if recoverRAVSignerFn == nil {
		return nil, fmt.Errorf("recoverRAVSigner function not found in ABI")
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

	data, err := recoverRAVSignerFn.NewCall(signedRAVTuple).Encode()
	if err != nil {
		return nil, fmt.Errorf("encoding recoverRAVSigner call: %w", err)
	}

	result, err := env.CallContract(env.Collector.Address, data)
	if err != nil {
		return nil, err
	}

	// Result is padded to 32 bytes, address is in last 20 bytes
	if len(result) >= 32 {
		return eth.Address(result[12:32]), nil
	}
	return nil, fmt.Errorf("unexpected result length: %d", len(result))
}

// ========== On-Chain Verification Tests ==========

func TestEIP712HashCompatibility(t *testing.T) {
	env := SetupEnv(t)

	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	collectionID := mustNewCollectionID("0xabababababababababababababababababababababababababababababababab")

	rav := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
		ServiceProvider: eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		DataService:     eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     1234567890123456789,
		ValueAggregate:  big.NewInt(1000000000000000000), // 1 ETH
		Metadata:        []byte{},
	}

	// Compute hash in Go
	goHash, err := horizon.HashTypedData(domain, rav)
	require.NoError(t, err)

	// Get hash from contract
	contractHash, err := env.CallEncodeRAV(rav)
	require.NoError(t, err)

	// They must match
	require.Equal(t, goHash[:], contractHash[:],
		"EIP-712 hash mismatch between Go (%s) and Solidity (%s)",
		hex.EncodeToString(goHash[:]),
		hex.EncodeToString(contractHash[:]))
}

func TestEIP712HashWithMetadata(t *testing.T) {
	env := SetupEnv(t)

	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	collectionID := mustNewCollectionID("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

	rav := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           eth.MustNewAddress("0x4444444444444444444444444444444444444444"),
		ServiceProvider: eth.MustNewAddress("0x5555555555555555555555555555555555555555"),
		DataService:     eth.MustNewAddress("0x6666666666666666666666666666666666666666"),
		TimestampNs:     9876543210987654321,
		ValueAggregate:  big.NewInt(5000000000000000000), // 5 ETH
		Metadata:        []byte("test metadata"),
	}

	goHash, err := horizon.HashTypedData(domain, rav)
	require.NoError(t, err)

	contractHash, err := env.CallEncodeRAV(rav)
	require.NoError(t, err)

	require.Equal(t, goHash[:], contractHash[:],
		"EIP-712 hash with metadata mismatch")
}

func TestSignatureRecoveryCompatibility(t *testing.T) {
	env := SetupEnv(t)

	key, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	expectedSigner := key.PublicKey().Address()

	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	collectionID := mustNewCollectionID("0xcafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe")

	rav := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           expectedSigner,
		ServiceProvider: eth.MustNewAddress("0x7777777777777777777777777777777777777777"),
		DataService:     eth.MustNewAddress("0x8888888888888888888888888888888888888888"),
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  big.NewInt(2000000000000000000),
		Metadata:        []byte{},
	}

	// Sign in Go
	signedRAV, err := horizon.Sign(domain, rav, key)
	require.NoError(t, err)

	// Verify in Go first
	goRecovered, err := signedRAV.RecoverSigner(domain)
	require.NoError(t, err)
	require.Equal(t, expectedSigner, goRecovered, "Go signature recovery failed")

	// First, let's verify the EIP-712 hash matches
	goHash, err := horizon.HashTypedData(domain, rav)
	require.NoError(t, err)
	contractHash, err := env.CallEncodeRAV(rav)
	require.NoError(t, err)
	t.Logf("Go EIP-712 hash:       %s", hex.EncodeToString(goHash[:]))
	t.Logf("Contract EIP-712 hash: %s", hex.EncodeToString(contractHash[:]))
	require.Equal(t, goHash[:], contractHash[:], "Hash mismatch between Go and contract")

	// Now try to recover on contract
	contractRecovered, err := env.CallRecoverRAVSigner(signedRAV)
	require.NoError(t, err, "recoverRAVSigner failed")

	require.Equal(t, expectedSigner, contractRecovered,
		"Signature recovery mismatch: Go recovered %s, contract recovered %s",
		expectedSigner.Pretty(), contractRecovered.Pretty())
}

// TestSignatureEncodingComparison compares the SignedRAV encoding from recoverRAVSigner vs collect
func TestSignatureEncodingComparison(t *testing.T) {
	env := SetupEnv(t)

	key, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	collectionID := mustNewCollectionID("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

	rav := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           key.PublicKey().Address(),
		ServiceProvider: eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		TimestampNs:     1234567890123456789,
		ValueAggregate:  big.NewInt(1000000000000000000),
		Metadata:        []byte{},
	}

	signedRAV, err := horizon.Sign(domain, rav, key)
	require.NoError(t, err)

	// Get the encoding from recoverRAVSigner
	recoverRAVSignerFn := env.Collector.ABI.FindFunctionByName("recoverRAVSigner")
	require.NotNil(t, recoverRAVSignerFn)

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

	recoverData, err := recoverRAVSignerFn.NewCall(signedRAVTuple).Encode()
	require.NoError(t, err)

	// Get the encoding from collectDataEncoder (synthetic ABI)
	collectData := encodeCollectData(signedRAV, 100000, eth.Address{})

	t.Logf("\n=== Encoding Comparison ===")
	t.Logf("recoverRAVSigner calldata length: %d", len(recoverData))
	t.Logf("collectData (bytes param) length: %d", len(collectData))

	// The recoverRAVSigner calldata should have:
	// - 4 bytes selector
	// - 32 bytes offset to SignedRAV
	// - SignedRAV data
	// Total SignedRAV data portion: len(recoverData) - 4 - 32 = len(recoverData) - 36

	// The collectData should have:
	// - 32 bytes offset to SignedRAV
	// - 32 bytes dataServiceCut
	// - 32 bytes receiverDestination
	// - SignedRAV data
	// The SignedRAV starts at offset specified in first 32 bytes

	// Extract SignedRAV portion from recoverData (skip selector + offset)
	// In recoverData: offset to SignedRAV is at bytes 4-35, value should be 0x20 (32)
	recoverOffset := new(big.Int).SetBytes(recoverData[4:36]).Uint64()
	t.Logf("recoverRAVSigner: offset to SignedRAV = %d (0x%x)", recoverOffset, recoverOffset)

	// In collectData: first 32 bytes is offset to SignedRAV
	collectOffset := new(big.Int).SetBytes(collectData[0:32]).Uint64()
	t.Logf("collectData: offset to SignedRAV = %d (0x%x)", collectOffset, collectOffset)

	// Extract SignedRAV head from both
	recoverSignedRAVHead := recoverData[4+int(recoverOffset) : 4+int(recoverOffset)+64]
	collectSignedRAVHead := collectData[int(collectOffset) : int(collectOffset)+64]

	t.Logf("\nrecoverRAVSigner SignedRAV head (first 64 bytes after offset):\n  %x", recoverSignedRAVHead)
	t.Logf("collectData SignedRAV head (first 64 bytes after offset):\n  %x", collectSignedRAVHead)

	// The SignedRAV head should be the same - it contains offsets to RAV and signature
	require.Equal(t, recoverSignedRAVHead, collectSignedRAVHead, "SignedRAV head encoding differs!")

	t.Logf("\nSignedRAV head is identical in both encodings")
}

// ========== Original Go-Only Tests ==========
// These test the Go implementation without contract interaction

func TestReceiptSigningAndRecovery(t *testing.T) {
	env := SetupEnv(t)

	key, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	expectedSigner := key.PublicKey().Address()

	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	collectionID := mustNewCollectionID("0xabababababababababababababababababababababababababababababababab")

	receipt := horizon.NewReceipt(
		collectionID,
		expectedSigner,
		eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
		eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		big.NewInt(1000),
	)

	signedReceipt, err := horizon.Sign(domain, receipt, key)
	require.NoError(t, err)

	recoveredSigner, err := signedReceipt.RecoverSigner(domain)
	require.NoError(t, err)
	require.Equal(t, expectedSigner, recoveredSigner)
}

func TestRAVAggregation(t *testing.T) {
	env := SetupEnv(t)

	senderKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	aggregatorKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	senderAddr := senderKey.PublicKey().Address()
	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	collectionID := mustNewCollectionID("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")

	payer := senderAddr
	dataService := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	serviceProvider := eth.MustNewAddress("0x2222222222222222222222222222222222222222")

	var receipts []*horizon.SignedReceipt
	totalValue := big.NewInt(0)

	for i := 0; i < 10; i++ {
		value := big.NewInt(int64(100 + i*10))

		receipt := &horizon.Receipt{
			CollectionID:    collectionID,
			Payer:           payer,
			DataService:     dataService,
			ServiceProvider: serviceProvider,
			TimestampNs:     uint64(time.Now().UnixNano()) + uint64(i),
			Nonce:           uint64(i),
			Value:           value,
		}

		signed, err := horizon.Sign(domain, receipt, senderKey)
		require.NoError(t, err)

		receipts = append(receipts, signed)
		totalValue.Add(totalValue, value)
	}

	aggregator := horizon.NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr})

	signedRAV, err := aggregator.AggregateReceipts(receipts, nil)
	require.NoError(t, err)

	require.Equal(t, collectionID, signedRAV.Message.CollectionID)
	require.Equal(t, payer, signedRAV.Message.Payer)
	require.Equal(t, serviceProvider, signedRAV.Message.ServiceProvider)
	require.Equal(t, dataService, signedRAV.Message.DataService)
	require.Equal(t, 0, signedRAV.Message.ValueAggregate.Cmp(totalValue))
}

func TestSignatureMalleabilityProtection(t *testing.T) {
	env := SetupEnv(t)

	key, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)

	var collectionID horizon.CollectionID
	receipt := &horizon.Receipt{
		CollectionID:    collectionID,
		Payer:           key.PublicKey().Address(),
		DataService:     eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
		ServiceProvider: eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		TimestampNs:     uint64(time.Now().UnixNano()),
		Nonce:           12345,
		Value:           big.NewInt(1000),
	}

	signed, err := horizon.Sign(domain, receipt, key)
	require.NoError(t, err)

	malleatedSig := createMalleatedSignature(signed.Signature)
	malleatedReceipt := &horizon.SignedReceipt{
		Message:   signed.Message,
		Signature: malleatedSig,
	}

	aggregatorKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	aggregator := horizon.NewAggregator(domain, aggregatorKey, []eth.Address{key.PublicKey().Address()})

	receipts := []*horizon.SignedReceipt{signed, malleatedReceipt}
	_, err = aggregator.AggregateReceipts(receipts, nil)
	require.ErrorIs(t, err, horizon.ErrDuplicateSignature)
}

func TestIncrementalRAVAggregation(t *testing.T) {
	env := SetupEnv(t)

	senderKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	aggregatorKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	senderAddr := senderKey.PublicKey().Address()
	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	aggregator := horizon.NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr, aggregatorKey.PublicKey().Address()})

	var collectionID horizon.CollectionID
	payer := senderAddr
	dataService := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	serviceProvider := eth.MustNewAddress("0x2222222222222222222222222222222222222222")

	var batch1 []*horizon.SignedReceipt
	baseTimestamp := uint64(time.Now().UnixNano())

	for i := range 5 {
		receipt := &horizon.Receipt{
			CollectionID:    collectionID,
			Payer:           payer,
			DataService:     dataService,
			ServiceProvider: serviceProvider,
			TimestampNs:     baseTimestamp + uint64(i),
			Nonce:           uint64(i),
			Value:           big.NewInt(100),
		}
		signed, err := horizon.Sign(domain, receipt, senderKey)
		require.NoError(t, err)
		batch1 = append(batch1, signed)
	}

	rav1, err := aggregator.AggregateReceipts(batch1, nil)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(500), rav1.Message.ValueAggregate)

	var batch2 []*horizon.SignedReceipt
	for i := range 5 {
		receipt := &horizon.Receipt{
			CollectionID:    collectionID,
			Payer:           payer,
			DataService:     dataService,
			ServiceProvider: serviceProvider,
			TimestampNs:     rav1.Message.TimestampNs + uint64(i) + 1,
			Nonce:           uint64(100 + i),
			Value:           big.NewInt(200),
		}
		signed, err := horizon.Sign(domain, receipt, senderKey)
		require.NoError(t, err)
		batch2 = append(batch2, signed)
	}

	rav2, err := aggregator.AggregateReceipts(batch2, rav1)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(1500), rav2.Message.ValueAggregate)
}

func TestReceiptTimestampValidation(t *testing.T) {
	env := SetupEnv(t)

	senderKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	aggregatorKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	senderAddr := senderKey.PublicKey().Address()
	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	aggregator := horizon.NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr, aggregatorKey.PublicKey().Address()})

	var collectionID horizon.CollectionID
	payer := senderAddr
	dataService := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	serviceProvider := eth.MustNewAddress("0x2222222222222222222222222222222222222222")

	baseTimestamp := uint64(time.Now().UnixNano())

	var initialReceipts []*horizon.SignedReceipt
	for i := range 3 {
		receipt := &horizon.Receipt{
			CollectionID:    collectionID,
			Payer:           payer,
			DataService:     dataService,
			ServiceProvider: serviceProvider,
			TimestampNs:     baseTimestamp + uint64(i),
			Nonce:           uint64(i),
			Value:           big.NewInt(100),
		}
		signed, err := horizon.Sign(domain, receipt, senderKey)
		require.NoError(t, err)
		initialReceipts = append(initialReceipts, signed)
	}

	rav1, err := aggregator.AggregateReceipts(initialReceipts, nil)
	require.NoError(t, err)

	oldReceipt := &horizon.Receipt{
		CollectionID:    collectionID,
		Payer:           payer,
		DataService:     dataService,
		ServiceProvider: serviceProvider,
		TimestampNs:     rav1.Message.TimestampNs,
		Nonce:           uint64(999),
		Value:           big.NewInt(100),
	}
	oldSigned, err := horizon.Sign(domain, oldReceipt, senderKey)
	require.NoError(t, err)

	_, err = aggregator.AggregateReceipts([]*horizon.SignedReceipt{oldSigned}, rav1)
	require.ErrorIs(t, err, horizon.ErrInvalidTimestamp)
}

func TestUnauthorizedSigner(t *testing.T) {
	env := SetupEnv(t)

	authorizedKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	unauthorizedKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	aggregatorKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	aggregator := horizon.NewAggregator(domain, aggregatorKey, []eth.Address{authorizedKey.PublicKey().Address()})

	var collectionID horizon.CollectionID
	receipt := &horizon.Receipt{
		CollectionID:    collectionID,
		Payer:           unauthorizedKey.PublicKey().Address(),
		DataService:     eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
		ServiceProvider: eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		TimestampNs:     uint64(time.Now().UnixNano()),
		Nonce:           1,
		Value:           big.NewInt(100),
	}

	signed, err := horizon.Sign(domain, receipt, unauthorizedKey)
	require.NoError(t, err)

	_, err = aggregator.AggregateReceipts([]*horizon.SignedReceipt{signed}, nil)
	require.ErrorIs(t, err, horizon.ErrInvalidSigner)
}

func TestCollectionIDMismatch(t *testing.T) {
	env := SetupEnv(t)

	senderKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	aggregatorKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	senderAddr := senderKey.PublicKey().Address()
	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	aggregator := horizon.NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr})

	payer := senderAddr
	dataService := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	serviceProvider := eth.MustNewAddress("0x2222222222222222222222222222222222222222")

	collectionID1 := mustNewCollectionID("0x1111111111111111111111111111111111111111111111111111111111111111")
	collectionID2 := mustNewCollectionID("0x2222222222222222222222222222222222222222222222222222222222222222")

	receipt1 := &horizon.Receipt{
		CollectionID:    collectionID1,
		Payer:           payer,
		DataService:     dataService,
		ServiceProvider: serviceProvider,
		TimestampNs:     uint64(time.Now().UnixNano()),
		Nonce:           1,
		Value:           big.NewInt(100),
	}

	receipt2 := &horizon.Receipt{
		CollectionID:    collectionID2,
		Payer:           payer,
		DataService:     dataService,
		ServiceProvider: serviceProvider,
		TimestampNs:     uint64(time.Now().UnixNano()) + 1,
		Nonce:           2,
		Value:           big.NewInt(100),
	}

	signed1, err := horizon.Sign(domain, receipt1, senderKey)
	require.NoError(t, err)
	signed2, err := horizon.Sign(domain, receipt2, senderKey)
	require.NoError(t, err)

	_, err = aggregator.AggregateReceipts([]*horizon.SignedReceipt{signed1, signed2}, nil)
	require.ErrorIs(t, err, horizon.ErrCollectionMismatch)
}

// Helper to create malleated (high-S) signature
func createMalleatedSignature(sig eth.Signature) eth.Signature {
	var result eth.Signature
	copy(result[:], sig[:])

	// secp256k1 curve order
	n, _ := new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)

	s := new(big.Int).SetBytes(sig[32:64])
	sNew := new(big.Int).Sub(n, s)

	sBytes := sNew.Bytes()
	for i := 32; i < 64; i++ {
		result[i] = 0
	}
	copy(result[64-len(sBytes):64], sBytes)
	result[64] ^= 1 // Flip V

	return result
}
