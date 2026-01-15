package integration

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/streamingfast/eth-go"
	horizon "github.com/streamingfast/horizon-go"
	"github.com/stretchr/testify/require"
)

// ========== Contract Call Helpers ==========

// domainSeparator() returns the EIP-712 domain separator
func (env *TestEnv) CallDomainSeparator() (eth.Hash, error) {
	domainSeparatorFn := env.ABIs.Collector.FindFunctionByName("domainSeparator")
	if domainSeparatorFn == nil {
		return nil, fmt.Errorf("domainSeparator function not found in ABI")
	}

	data, err := domainSeparatorFn.NewCall().Encode()
	if err != nil {
		return nil, fmt.Errorf("encoding domainSeparator call: %w", err)
	}

	result, err := env.CallContract(env.CollectorAddress, data)
	if err != nil {
		return nil, err
	}

	// eth.Hash is []byte, so just return the result directly
	return eth.Hash(result), nil
}

// encodeRAV(ReceiptAggregateVoucher calldata rav) returns the EIP-712 hash
// Function selector from our contract
func (env *TestEnv) CallEncodeRAV(rav *horizon.RAV) (eth.Hash, error) {
	// Function selector for encodeRAV(tuple)
	// encodeRAV((bytes32,address,address,address,uint64,uint128,bytes))
	selector, _ := hex.DecodeString("26969c4c")

	// For calldata structs containing dynamic types (bytes), ABI encoding is:
	// 1. Offset to the tuple (32 bytes pointing to slot 1 = 0x20 = 32)
	// 2. Tuple content starting at that offset
	buf := make([]byte, 0, 512)

	// Selector
	buf = append(buf, selector...)

	// Offset to tuple = 32 (0x20)
	buf = append(buf, padLeft(big.NewInt(32).Bytes(), 32)...)

	// Now the tuple content:
	// For a tuple with dynamic bytes, we need:
	// - 6 fixed fields (192 bytes)
	// - offset to metadata (32 bytes)
	// - metadata length (32 bytes)
	// - metadata content (padded)

	// collectionId (bytes32)
	buf = append(buf, rav.CollectionID[:]...)

	// payer (address)
	buf = append(buf, padLeft(rav.Payer[:], 32)...)

	// serviceProvider (address)
	buf = append(buf, padLeft(rav.ServiceProvider[:], 32)...)

	// dataService (address)
	buf = append(buf, padLeft(rav.DataService[:], 32)...)

	// timestampNs (uint64)
	timestampBytes := make([]byte, 32)
	binary.BigEndian.PutUint64(timestampBytes[24:], rav.TimestampNs)
	buf = append(buf, timestampBytes...)

	// valueAggregate (uint128)
	buf = append(buf, padLeft(rav.ValueAggregate.Bytes(), 32)...)

	// metadata offset within tuple = 7*32 = 224 (points past the 7 fixed slots)
	buf = append(buf, padLeft(big.NewInt(7*32).Bytes(), 32)...)

	// metadata length
	buf = append(buf, padLeft(big.NewInt(int64(len(rav.Metadata))).Bytes(), 32)...)

	// metadata content (padded to 32 bytes)
	if len(rav.Metadata) > 0 {
		paddedMetadata := make([]byte, ((len(rav.Metadata)+31)/32)*32)
		copy(paddedMetadata, rav.Metadata)
		buf = append(buf, paddedMetadata...)
	}

	result, err := env.CallContract(env.CollectorAddress, buf)
	if err != nil {
		return nil, err
	}

	// eth.Hash is []byte, so just return the result directly
	return eth.Hash(result), nil
}

// recoverRAVSigner(SignedRAV calldata signedRAV) returns the signer address
func (env *TestEnv) CallRecoverRAVSigner(signedRAV *horizon.SignedRAV) (eth.Address, error) {
	// Function selector for recoverRAVSigner(((bytes32,address,address,address,uint64,uint128,bytes),bytes))
	selector, _ := hex.DecodeString("63648817")

	// Build dynamic ABI encoding for the SignedRAV struct
	// This is complex due to nested dynamic types
	data := encodeSignedRAVForCall(selector, signedRAV)

	result, err := env.CallContract(env.CollectorAddress, data)
	if err != nil {
		return nil, err
	}

	// Result is padded to 32 bytes, address is in last 20 bytes
	if len(result) >= 32 {
		return eth.Address(result[12:32]), nil
	}
	return nil, fmt.Errorf("unexpected result length: %d", len(result))
}

// encodeSignedRAVForCall encodes a SignedRAV for contract call
func encodeSignedRAVForCall(selector []byte, signedRAV *horizon.SignedRAV) []byte {
	rav := signedRAV.Message

	// Calculate offsets for dynamic data
	// SignedRAV has two fields: rav (tuple with dynamic bytes) and signature (bytes)
	// We need to encode:
	// 1. offset to SignedRAV tuple (points to slot 32 = 0x20)
	// 2. SignedRAV tuple:
	//    - offset to rav tuple
	//    - offset to signature
	//    - rav tuple content
	//    - signature content

	buf := make([]byte, 0, 1024)

	// Selector
	buf = append(buf, selector...)

	// Offset to SignedRAV tuple (32)
	buf = append(buf, padLeft(big.NewInt(32).Bytes(), 32)...)

	// Now encode SignedRAV tuple
	// First: offset to rav (within SignedRAV) - starts at slot 2 (64 bytes after start of SignedRAV)
	buf = append(buf, padLeft(big.NewInt(64).Bytes(), 32)...) // rav starts at 64

	// Second: offset to signature (within SignedRAV)
	// Signature starts after rav content
	// RAV content: 7 fixed slots (224) + metadata length slot (32) + padded metadata data
	ravContentSize := int64(7*32 + 32) // 7 fixed slots + metadata length slot = 256
	if len(rav.Metadata) > 0 {
		ravContentSize += int64(((len(rav.Metadata) + 31) / 32) * 32)
	}
	sigOffset := 64 + ravContentSize
	buf = append(buf, padLeft(big.NewInt(sigOffset).Bytes(), 32)...)

	// Now encode rav tuple content
	// collectionId
	buf = append(buf, rav.CollectionID[:]...)
	// payer
	buf = append(buf, padLeft(rav.Payer[:], 32)...)
	// serviceProvider
	buf = append(buf, padLeft(rav.ServiceProvider[:], 32)...)
	// dataService
	buf = append(buf, padLeft(rav.DataService[:], 32)...)
	// timestampNs
	timestampBytes := make([]byte, 32)
	binary.BigEndian.PutUint64(timestampBytes[24:], rav.TimestampNs)
	buf = append(buf, timestampBytes...)
	// valueAggregate
	buf = append(buf, padLeft(rav.ValueAggregate.Bytes(), 32)...)
	// metadata offset within rav tuple (7*32 = 224)
	buf = append(buf, padLeft(big.NewInt(7*32).Bytes(), 32)...)
	// metadata length
	buf = append(buf, padLeft(big.NewInt(int64(len(rav.Metadata))).Bytes(), 32)...)
	// metadata content (padded)
	if len(rav.Metadata) > 0 {
		paddedMetadata := make([]byte, ((len(rav.Metadata)+31)/32)*32)
		copy(paddedMetadata, rav.Metadata)
		buf = append(buf, paddedMetadata...)
	}

	// Now encode signature
	// signature length
	buf = append(buf, padLeft(big.NewInt(int64(len(signedRAV.Signature))).Bytes(), 32)...)
	// signature content (padded)
	// Note: eth.Signature is in V+R+S format, but Solidity ECDSA expects R+S+V format
	// We need to reorder the bytes
	paddedSig := make([]byte, ((len(signedRAV.Signature)+31)/32)*32)
	sig := signedRAV.Signature
	// Reorder from V+R+S to R+S+V
	copy(paddedSig[0:32], sig[1:33])   // R (32 bytes)
	copy(paddedSig[32:64], sig[33:65]) // S (32 bytes)
	paddedSig[64] = sig[0]             // V (1 byte)
	buf = append(buf, paddedSig...)

	return buf
}

func padLeft(b []byte, size int) []byte {
	if len(b) >= size {
		return b[len(b)-size:]
	}
	result := make([]byte, size)
	copy(result[size-len(b):], b)
	return result
}

// ========== On-Chain Verification Tests ==========

func TestDomainSeparatorCompatibility(t *testing.T) {
	env := SetupEnv(t)

	// Compute domain separator in Go
	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)
	goDomainSep := domain.Separator()

	// Get domain separator from contract
	contractDomainSep, err := env.CallDomainSeparator()
	require.NoError(t, err)

	// They must match
	require.Equal(t, goDomainSep[:], contractDomainSep[:],
		"Domain separator mismatch between Go (%s) and Solidity (%s)",
		hex.EncodeToString(goDomainSep[:]),
		hex.EncodeToString(contractDomainSep[:]))
}

func TestEIP712HashCompatibility(t *testing.T) {
	env := SetupEnv(t)

	// Create domain
	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	// Create RAV with known values
	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0xabababababababababababababababababababababababababababababababab")[:])

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

	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")[:])

	// Test with non-empty metadata
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

	// Generate test key
	key, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	expectedSigner := key.PublicKey().Address()

	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0xcafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe")[:])

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

	// Verify on contract
	contractRecovered, err := env.CallRecoverRAVSigner(signedRAV)
	require.NoError(t, err)

	require.Equal(t, expectedSigner, contractRecovered,
		"Signature recovery mismatch: Go recovered %s, contract recovered %s",
		expectedSigner.Pretty(), contractRecovered.Pretty())
}

// ========== Original Go-Only Tests ==========
// These test the Go implementation without contract interaction

func TestReceiptSigningAndRecovery(t *testing.T) {
	env := SetupEnv(t)

	key, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	expectedSigner := key.PublicKey().Address()

	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0xabababababababababababababababababababababababababababababababab")[:])

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

	senderKey, _ := eth.NewRandomPrivateKey()
	aggregatorKey, _ := eth.NewRandomPrivateKey()

	senderAddr := senderKey.PublicKey().Address()

	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")[:])

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

	key, _ := eth.NewRandomPrivateKey()
	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

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

	signed, _ := horizon.Sign(domain, receipt, key)

	malleatedSig := createMalleatedSignature(signed.Signature)
	malleatedReceipt := &horizon.SignedReceipt{
		Message:   signed.Message,
		Signature: malleatedSig,
	}

	aggregatorKey, _ := eth.NewRandomPrivateKey()
	aggregator := horizon.NewAggregator(domain, aggregatorKey, []eth.Address{key.PublicKey().Address()})

	receipts := []*horizon.SignedReceipt{signed, malleatedReceipt}
	_, err := aggregator.AggregateReceipts(receipts, nil)
	require.ErrorIs(t, err, horizon.ErrDuplicateSignature)
}

func TestIncrementalRAVAggregation(t *testing.T) {
	env := SetupEnv(t)

	senderKey, _ := eth.NewRandomPrivateKey()
	aggregatorKey, _ := eth.NewRandomPrivateKey()

	senderAddr := senderKey.PublicKey().Address()
	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)
	aggregator := horizon.NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr, aggregatorKey.PublicKey().Address()})

	var collectionID horizon.CollectionID
	payer := senderAddr
	dataService := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	serviceProvider := eth.MustNewAddress("0x2222222222222222222222222222222222222222")

	var batch1 []*horizon.SignedReceipt
	baseTimestamp := uint64(time.Now().UnixNano())

	for i := 0; i < 5; i++ {
		receipt := &horizon.Receipt{
			CollectionID:    collectionID,
			Payer:           payer,
			DataService:     dataService,
			ServiceProvider: serviceProvider,
			TimestampNs:     baseTimestamp + uint64(i),
			Nonce:           uint64(i),
			Value:           big.NewInt(100),
		}
		signed, _ := horizon.Sign(domain, receipt, senderKey)
		batch1 = append(batch1, signed)
	}

	rav1, err := aggregator.AggregateReceipts(batch1, nil)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(500), rav1.Message.ValueAggregate)

	var batch2 []*horizon.SignedReceipt
	for i := 0; i < 5; i++ {
		receipt := &horizon.Receipt{
			CollectionID:    collectionID,
			Payer:           payer,
			DataService:     dataService,
			ServiceProvider: serviceProvider,
			TimestampNs:     rav1.Message.TimestampNs + uint64(i) + 1,
			Nonce:           uint64(100 + i),
			Value:           big.NewInt(200),
		}
		signed, _ := horizon.Sign(domain, receipt, senderKey)
		batch2 = append(batch2, signed)
	}

	rav2, err := aggregator.AggregateReceipts(batch2, rav1)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(1500), rav2.Message.ValueAggregate)
}

func TestReceiptTimestampValidation(t *testing.T) {
	env := SetupEnv(t)

	senderKey, _ := eth.NewRandomPrivateKey()
	aggregatorKey, _ := eth.NewRandomPrivateKey()

	senderAddr := senderKey.PublicKey().Address()
	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)
	aggregator := horizon.NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr, aggregatorKey.PublicKey().Address()})

	var collectionID horizon.CollectionID
	payer := senderAddr
	dataService := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	serviceProvider := eth.MustNewAddress("0x2222222222222222222222222222222222222222")

	baseTimestamp := uint64(time.Now().UnixNano())

	var initialReceipts []*horizon.SignedReceipt
	for i := 0; i < 3; i++ {
		receipt := &horizon.Receipt{
			CollectionID:    collectionID,
			Payer:           payer,
			DataService:     dataService,
			ServiceProvider: serviceProvider,
			TimestampNs:     baseTimestamp + uint64(i),
			Nonce:           uint64(i),
			Value:           big.NewInt(100),
		}
		signed, _ := horizon.Sign(domain, receipt, senderKey)
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
	oldSigned, _ := horizon.Sign(domain, oldReceipt, senderKey)

	_, err = aggregator.AggregateReceipts([]*horizon.SignedReceipt{oldSigned}, rav1)
	require.ErrorIs(t, err, horizon.ErrInvalidTimestamp)
}

func TestUnauthorizedSigner(t *testing.T) {
	env := SetupEnv(t)

	authorizedKey, _ := eth.NewRandomPrivateKey()
	unauthorizedKey, _ := eth.NewRandomPrivateKey()
	aggregatorKey, _ := eth.NewRandomPrivateKey()

	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

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

	signed, _ := horizon.Sign(domain, receipt, unauthorizedKey)

	_, err := aggregator.AggregateReceipts([]*horizon.SignedReceipt{signed}, nil)
	require.ErrorIs(t, err, horizon.ErrInvalidSigner)
}

func TestCollectionIDMismatch(t *testing.T) {
	env := SetupEnv(t)

	senderKey, _ := eth.NewRandomPrivateKey()
	aggregatorKey, _ := eth.NewRandomPrivateKey()

	senderAddr := senderKey.PublicKey().Address()
	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)
	aggregator := horizon.NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr})

	payer := senderAddr
	dataService := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	serviceProvider := eth.MustNewAddress("0x2222222222222222222222222222222222222222")

	var collectionID1 horizon.CollectionID
	copy(collectionID1[:], eth.MustNewHash("0x1111111111111111111111111111111111111111111111111111111111111111")[:])

	var collectionID2 horizon.CollectionID
	copy(collectionID2[:], eth.MustNewHash("0x2222222222222222222222222222222222222222222222222222222222222222")[:])

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

	signed1, _ := horizon.Sign(domain, receipt1, senderKey)
	signed2, _ := horizon.Sign(domain, receipt2, senderKey)

	_, err := aggregator.AggregateReceipts([]*horizon.SignedReceipt{signed1, signed2}, nil)
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
