package integration

import (
	"math/big"
	"testing"
	"time"

	"github.com/streamingfast/eth-go"
	"github.com/streamingfast/horizon-go"
	"github.com/stretchr/testify/require"
)

func TestReceiptSigningAndRecovery(t *testing.T) {
	env := SetupEnv(t)

	// Generate test wallet
	key, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	expectedSigner := key.PublicKey().Address()

	// Create domain using deployed contract address
	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	// Create receipt
	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0xabababababababababababababababababababababababababababababababab")[:])

	receipt := horizon.NewReceipt(
		collectionID,
		expectedSigner,
		eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
		eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		big.NewInt(1000),
	)

	// Sign
	signedReceipt, err := horizon.Sign(domain, receipt, key)
	require.NoError(t, err)

	// Recover and verify
	recoveredSigner, err := signedReceipt.RecoverSigner(domain)
	require.NoError(t, err)
	require.Equal(t, expectedSigner, recoveredSigner)
}

func TestRAVAggregation(t *testing.T) {
	env := SetupEnv(t)

	// Generate keys
	senderKey, _ := eth.NewRandomPrivateKey()
	aggregatorKey, _ := eth.NewRandomPrivateKey()

	senderAddr := senderKey.PublicKey().Address()

	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	// Create collection ID
	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")[:])

	payer := senderAddr
	dataService := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	serviceProvider := eth.MustNewAddress("0x2222222222222222222222222222222222222222")

	// Create multiple receipts
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

	// Create aggregator
	aggregator := horizon.NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr})

	// Aggregate
	signedRAV, err := aggregator.AggregateReceipts(receipts, nil)
	require.NoError(t, err)

	// Verify RAV properties
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

	// Create malleated signature (high-S form)
	// Note: malleated signatures may recover to different addresses,
	// but our UniqueID normalization will detect them as duplicates
	malleatedSig := createMalleatedSignature(signed.Signature)
	malleatedReceipt := &horizon.SignedReceipt{
		Message:   signed.Message,
		Signature: malleatedSig,
	}

	// The key point: aggregator should detect malleated signature as duplicate
	// even though it may recover to a different address
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

	// First batch of receipts
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

	// First RAV
	rav1, err := aggregator.AggregateReceipts(batch1, nil)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(500), rav1.Message.ValueAggregate)

	// Second batch (timestamps must be > first RAV's timestamp)
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

	// Second RAV (incremental)
	rav2, err := aggregator.AggregateReceipts(batch2, rav1)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(1500), rav2.Message.ValueAggregate) // 500 + 1000
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

	// Create initial receipts and RAV
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

	// Try to aggregate receipt with timestamp <= previous RAV timestamp
	oldReceipt := &horizon.Receipt{
		CollectionID:    collectionID,
		Payer:           payer,
		DataService:     dataService,
		ServiceProvider: serviceProvider,
		TimestampNs:     rav1.Message.TimestampNs, // Same timestamp - should fail
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

	// Only authorize one key
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

	// Sign with unauthorized key
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
		CollectionID:    collectionID2, // Different collection ID
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
