package horizon

import (
	"math/big"
	"testing"

	"github.com/streamingfast/eth-go"
	"github.com/stretchr/testify/require"
)

func TestDomain_Separator(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")

	domain := NewDomain(chainID, verifyingContract)

	require.Equal(t, "GraphTallyCollector", domain.Name)
	require.Equal(t, "1", domain.Version)
	require.Equal(t, int64(chainID), domain.ChainID.Int64())
	require.True(t, addressesEqual(verifyingContract, domain.VerifyingContract))

	// Compute separator
	separator := domain.Separator()

	// Should be deterministic
	separator2 := domain.Separator()
	require.Equal(t, separator, separator2)

	// Should be 32 bytes
	require.Equal(t, 32, len(separator))
}

func TestReceipt_EIP712Encoding(t *testing.T) {
	var collectionID CollectionID
	copy(collectionID[:], eth.MustNewHash("0xabababababababababababababababababababababababababababababababab")[:])

	receipt := &Receipt{
		CollectionID:    collectionID,
		Payer:           eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     1234567890,
		Nonce:           999,
		Value:           big.NewInt(1000),
	}

	// Check type hash
	typeHash := receipt.EIP712TypeHash()
	require.Equal(t, 32, len(typeHash))

	// Type hash should be deterministic
	expectedTypeHash := keccak256([]byte(
		"Receipt(bytes32 collection_id,address payer,address data_service,address service_provider,uint64 timestamp_ns,uint64 nonce,uint128 value)"))
	require.Equal(t, expectedTypeHash, typeHash)

	// Check encoded data
	encodedData := receipt.EIP712EncodeData()
	require.Equal(t, 32*7, len(encodedData)) // 7 fields, 32 bytes each
}

func TestRAV_EIP712Encoding(t *testing.T) {
	var collectionID CollectionID
	copy(collectionID[:], eth.MustNewHash("0xabababababababababababababababababababababababababababababababab")[:])

	rav := &RAV{
		CollectionID:    collectionID,
		Payer:           eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		TimestampNs:     1234567890,
		ValueAggregate:  big.NewInt(5000),
		Metadata:        []byte{1, 2, 3},
	}

	// Check type hash
	typeHash := rav.EIP712TypeHash()
	require.Equal(t, 32, len(typeHash))

	// Type hash should be deterministic
	expectedTypeHash := keccak256([]byte(
		"ReceiptAggregateVoucher(bytes32 collectionId,address payer,address serviceProvider,address dataService,uint64 timestampNs,uint128 valueAggregate,bytes metadata)"))
	require.Equal(t, expectedTypeHash, typeHash)

	// Check encoded data
	encodedData := rav.EIP712EncodeData()
	require.Equal(t, 32*7, len(encodedData)) // 7 fields, 32 bytes each
}

func TestHashTypedData_Receipt(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := NewDomain(chainID, verifyingContract)

	var collectionID CollectionID
	receipt := &Receipt{
		CollectionID:    collectionID,
		Payer:           eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     1234567890,
		Nonce:           999,
		Value:           big.NewInt(1000),
	}

	hash, err := HashTypedData(domain, receipt)
	require.NoError(t, err)
	require.Equal(t, 32, len(hash))

	// Should be deterministic
	hash2, err := HashTypedData(domain, receipt)
	require.NoError(t, err)
	require.Equal(t, hash, hash2)

	// Different receipt should have different hash
	receipt2 := &Receipt{
		CollectionID:    collectionID,
		Payer:           eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     1234567890,
		Nonce:           999,
		Value:           big.NewInt(2000), // Different value
	}

	hash3, err := HashTypedData(domain, receipt2)
	require.NoError(t, err)
	require.NotEqual(t, hash, hash3)
}

func TestHashTypedData_RAV(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := NewDomain(chainID, verifyingContract)

	var collectionID CollectionID
	rav := &RAV{
		CollectionID:    collectionID,
		Payer:           eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		TimestampNs:     1234567890,
		ValueAggregate:  big.NewInt(5000),
		Metadata:        []byte{},
	}

	hash, err := HashTypedData(domain, rav)
	require.NoError(t, err)
	require.Equal(t, 32, len(hash))

	// Should be deterministic
	hash2, err := HashTypedData(domain, rav)
	require.NoError(t, err)
	require.Equal(t, hash, hash2)
}

func TestEncoding_Helpers(t *testing.T) {
	// Test padLeft
	t.Run("padLeft", func(t *testing.T) {
		b := []byte{1, 2, 3}
		padded := padLeft(b, 5)
		require.Equal(t, 5, len(padded))
		require.Equal(t, []byte{0, 0, 1, 2, 3}, padded)

		// Already large enough
		b2 := []byte{1, 2, 3, 4, 5, 6}
		padded2 := padLeft(b2, 5)
		require.Equal(t, 5, len(padded2))
		require.Equal(t, []byte{2, 3, 4, 5, 6}, padded2) // Takes last 5
	})

	// Test encodeUint64
	t.Run("encodeUint64", func(t *testing.T) {
		encoded := encodeUint64(0x123456789ABCDEF0)
		require.Equal(t, 32, len(encoded))
		// Check last 8 bytes contain the value
		require.Equal(t, byte(0x12), encoded[24])
		require.Equal(t, byte(0xF0), encoded[31])
	})

	// Test encodeUint128
	t.Run("encodeUint128", func(t *testing.T) {
		value := big.NewInt(12345)
		encoded := encodeUint128(value)
		require.Equal(t, 32, len(encoded))

		// Decode and verify
		decoded := new(big.Int).SetBytes(encoded)
		require.Equal(t, 0, value.Cmp(decoded))
	})

	// Test encodeUint128 with nil
	t.Run("encodeUint128_nil", func(t *testing.T) {
		encoded := encodeUint128(nil)
		require.Equal(t, 32, len(encoded))
		// Should be all zeros
		for _, b := range encoded {
			require.Equal(t, byte(0), b)
		}
	})
}
