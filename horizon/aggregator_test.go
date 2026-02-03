package horizon

import (
	"math/big"
	"testing"
	"time"

	"github.com/streamingfast/eth-go"
	"github.com/stretchr/testify/require"
)

func TestAggregator_SimpleAggregation(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := NewDomain(chainID, verifyingContract)

	// Generate keys
	senderKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	aggregatorKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	senderAddr := senderKey.PublicKey().Address()

	// Create aggregator
	aggregator := NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr})

	// Create receipts
	var collectionID CollectionID
	payer := senderAddr
	dataService := eth.MustNewAddress("0x2222222222222222222222222222222222222222")
	serviceProvider := eth.MustNewAddress("0x3333333333333333333333333333333333333333")

	var receipts []*SignedReceipt
	totalValue := big.NewInt(0)

	for i := 0; i < 5; i++ {
		value := big.NewInt(int64(100 + i*10))
		receipt := &Receipt{
			CollectionID:    collectionID,
			Payer:           payer,
			DataService:     dataService,
			ServiceProvider: serviceProvider,
			TimestampNs:     uint64(time.Now().UnixNano()) + uint64(i),
			Nonce:           uint64(i),
			Value:           value,
		}

		signed, err := Sign(domain, receipt, senderKey)
		require.NoError(t, err)

		receipts = append(receipts, signed)
		totalValue.Add(totalValue, value)
	}

	// Aggregate
	signedRAV, err := aggregator.AggregateReceipts(receipts, nil)
	require.NoError(t, err)
	require.NotNil(t, signedRAV)

	// Verify RAV
	rav := signedRAV.Message
	require.Equal(t, collectionID, rav.CollectionID)
	require.True(t, addressesEqual(payer, rav.Payer))
	require.True(t, addressesEqual(serviceProvider, rav.ServiceProvider))
	require.True(t, addressesEqual(dataService, rav.DataService))
	require.Equal(t, 0, totalValue.Cmp(rav.ValueAggregate))

	// Verify RAV signer
	ravSigner, err := signedRAV.RecoverSigner(domain)
	require.NoError(t, err)
	require.True(t, addressesEqual(aggregatorKey.PublicKey().Address(), ravSigner))
}

func TestAggregator_IncrementalAggregation(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := NewDomain(chainID, verifyingContract)

	senderKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	aggregatorKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	senderAddr := senderKey.PublicKey().Address()
	aggregatorAddr := aggregatorKey.PublicKey().Address()

	// Aggregator accepts both sender and itself (for RAV verification)
	aggregator := NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr, aggregatorAddr})

	var collectionID CollectionID
	payer := senderAddr
	dataService := eth.MustNewAddress("0x2222222222222222222222222222222222222222")
	serviceProvider := eth.MustNewAddress("0x3333333333333333333333333333333333333333")

	// First batch
	var batch1 []*SignedReceipt
	baseTimestamp := uint64(time.Now().UnixNano())
	for i := 0; i < 3; i++ {
		receipt := &Receipt{
			CollectionID:    collectionID,
			Payer:           payer,
			DataService:     dataService,
			ServiceProvider: serviceProvider,
			TimestampNs:     baseTimestamp + uint64(i),
			Nonce:           uint64(i),
			Value:           big.NewInt(100),
		}
		signed, err := Sign(domain, receipt, senderKey)
		require.NoError(t, err)
		batch1 = append(batch1, signed)
	}

	rav1, err := aggregator.AggregateReceipts(batch1, nil)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(300), rav1.Message.ValueAggregate)

	// Second batch (must have timestamps > rav1.TimestampNs)
	var batch2 []*SignedReceipt
	for i := 0; i < 2; i++ {
		receipt := &Receipt{
			CollectionID:    collectionID,
			Payer:           payer,
			DataService:     dataService,
			ServiceProvider: serviceProvider,
			TimestampNs:     rav1.Message.TimestampNs + uint64(i) + 1,
			Nonce:           uint64(100 + i),
			Value:           big.NewInt(200),
		}
		signed, err := Sign(domain, receipt, senderKey)
		require.NoError(t, err)
		batch2 = append(batch2, signed)
	}

	rav2, err := aggregator.AggregateReceipts(batch2, rav1)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(700), rav2.Message.ValueAggregate) // 300 + 400
	require.Greater(t, rav2.Message.TimestampNs, rav1.Message.TimestampNs)
}

func TestAggregator_DuplicateSignature(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := NewDomain(chainID, verifyingContract)

	senderKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	aggregatorKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	aggregator := NewAggregator(domain, aggregatorKey, []eth.Address{senderKey.PublicKey().Address()})

	var collectionID CollectionID
	receipt := &Receipt{
		CollectionID:    collectionID,
		Payer:           senderKey.PublicKey().Address(),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     uint64(time.Now().UnixNano()),
		Nonce:           1,
		Value:           big.NewInt(100),
	}

	signed, err := Sign(domain, receipt, senderKey)
	require.NoError(t, err)

	// Try to aggregate with duplicate
	receipts := []*SignedReceipt{signed, signed}
	_, err = aggregator.AggregateReceipts(receipts, nil)
	require.ErrorIs(t, err, ErrDuplicateSignature)
}

func TestAggregator_InvalidTimestamp(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := NewDomain(chainID, verifyingContract)

	senderKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	aggregatorKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	senderAddr := senderKey.PublicKey().Address()
	aggregatorAddr := aggregatorKey.PublicKey().Address()
	aggregator := NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr, aggregatorAddr})

	var collectionID CollectionID
	baseTimestamp := uint64(time.Now().UnixNano())

	// Create initial RAV
	receipt1 := &Receipt{
		CollectionID:    collectionID,
		Payer:           senderAddr,
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     baseTimestamp,
		Nonce:           1,
		Value:           big.NewInt(100),
	}
	signed1, err := Sign(domain, receipt1, senderKey)
	require.NoError(t, err)

	rav1, err := aggregator.AggregateReceipts([]*SignedReceipt{signed1}, nil)
	require.NoError(t, err)

	// Try to aggregate receipt with same timestamp
	receipt2 := &Receipt{
		CollectionID:    collectionID,
		Payer:           senderAddr,
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     rav1.Message.TimestampNs, // Same timestamp
		Nonce:           2,
		Value:           big.NewInt(100),
	}
	signed2, err := Sign(domain, receipt2, senderKey)
	require.NoError(t, err)

	_, err = aggregator.AggregateReceipts([]*SignedReceipt{signed2}, rav1)
	require.ErrorIs(t, err, ErrInvalidTimestamp)
}

func TestAggregator_UnauthorizedSigner(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := NewDomain(chainID, verifyingContract)

	authorizedKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	unauthorizedKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	aggregatorKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	// Only authorize one key
	aggregator := NewAggregator(domain, aggregatorKey, []eth.Address{authorizedKey.PublicKey().Address()})

	var collectionID CollectionID
	receipt := &Receipt{
		CollectionID:    collectionID,
		Payer:           unauthorizedKey.PublicKey().Address(),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     uint64(time.Now().UnixNano()),
		Nonce:           1,
		Value:           big.NewInt(100),
	}

	// Sign with unauthorized key
	signed, err := Sign(domain, receipt, unauthorizedKey)
	require.NoError(t, err)

	_, err = aggregator.AggregateReceipts([]*SignedReceipt{signed}, nil)
	require.ErrorIs(t, err, ErrInvalidSigner)
}

func TestAggregator_CollectionMismatch(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := NewDomain(chainID, verifyingContract)

	senderKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	aggregatorKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	aggregator := NewAggregator(domain, aggregatorKey, []eth.Address{senderKey.PublicKey().Address()})

	var collectionID1, collectionID2 CollectionID
	copy(collectionID1[:], eth.MustNewHash("0x1111111111111111111111111111111111111111111111111111111111111111")[:])
	copy(collectionID2[:], eth.MustNewHash("0x2222222222222222222222222222222222222222222222222222222222222222")[:])

	receipt1 := &Receipt{
		CollectionID:    collectionID1,
		Payer:           senderKey.PublicKey().Address(),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     uint64(time.Now().UnixNano()),
		Nonce:           1,
		Value:           big.NewInt(100),
	}

	receipt2 := &Receipt{
		CollectionID:    collectionID2, // Different
		Payer:           senderKey.PublicKey().Address(),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     uint64(time.Now().UnixNano()) + 1,
		Nonce:           2,
		Value:           big.NewInt(100),
	}

	signed1, err := Sign(domain, receipt1, senderKey)
	require.NoError(t, err)
	signed2, err := Sign(domain, receipt2, senderKey)
	require.NoError(t, err)

	_, err = aggregator.AggregateReceipts([]*SignedReceipt{signed1, signed2}, nil)
	require.ErrorIs(t, err, ErrCollectionMismatch)
}

func TestAggregator_AggregateOverflow(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := NewDomain(chainID, verifyingContract)

	senderKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	aggregatorKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	aggregator := NewAggregator(domain, aggregatorKey, []eth.Address{senderKey.PublicKey().Address()})

	var collectionID CollectionID

	// Create receipt with max value
	receipt1 := &Receipt{
		CollectionID:    collectionID,
		Payer:           senderKey.PublicKey().Address(),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     uint64(time.Now().UnixNano()),
		Nonce:           1,
		Value:           new(big.Int).Set(MaxUint128),
	}

	// Create another receipt that would overflow
	receipt2 := &Receipt{
		CollectionID:    collectionID,
		Payer:           senderKey.PublicKey().Address(),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     uint64(time.Now().UnixNano()) + 1,
		Nonce:           2,
		Value:           big.NewInt(1),
	}

	signed1, err := Sign(domain, receipt1, senderKey)
	require.NoError(t, err)
	signed2, err := Sign(domain, receipt2, senderKey)
	require.NoError(t, err)

	_, err = aggregator.AggregateReceipts([]*SignedReceipt{signed1, signed2}, nil)
	require.ErrorIs(t, err, ErrAggregateOverflow)
}

func TestAggregator_NoReceipts(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := NewDomain(chainID, verifyingContract)

	aggregatorKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	aggregator := NewAggregator(domain, aggregatorKey, []eth.Address{})

	_, err = aggregator.AggregateReceipts([]*SignedReceipt{}, nil)
	require.ErrorIs(t, err, ErrNoReceipts)
}
