package horizon

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/streamingfast/eth-go"
	"github.com/stretchr/testify/require"
)

func TestCollectionID_JSON(t *testing.T) {
	var id CollectionID
	copy(id[:], eth.MustNewHash("0xabababababababababababababababababababababababababababababababab")[:])

	// Marshal
	data, err := json.Marshal(id)
	require.NoError(t, err)

	// Unmarshal
	var decoded CollectionID
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	require.Equal(t, id, decoded)
}

func TestNewReceipt(t *testing.T) {
	var collectionID CollectionID
	copy(collectionID[:], eth.MustNewHash("0x1111111111111111111111111111111111111111111111111111111111111111")[:])

	payer := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	dataService := eth.MustNewAddress("0x2222222222222222222222222222222222222222")
	serviceProvider := eth.MustNewAddress("0x3333333333333333333333333333333333333333")
	value := big.NewInt(1000)

	receipt := NewReceipt(collectionID, payer, dataService, serviceProvider, value)

	require.Equal(t, collectionID, receipt.CollectionID)
	require.True(t, addressesEqual(payer, receipt.Payer))
	require.True(t, addressesEqual(dataService, receipt.DataService))
	require.True(t, addressesEqual(serviceProvider, receipt.ServiceProvider))
	require.Equal(t, 0, receipt.Value.Cmp(value))
	require.NotZero(t, receipt.TimestampNs)
	require.NotZero(t, receipt.Nonce)
}

func TestReceipt_JSON(t *testing.T) {
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

	// Marshal
	data, err := json.Marshal(receipt)
	require.NoError(t, err)

	// Unmarshal
	var decoded Receipt
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	require.Equal(t, receipt.CollectionID, decoded.CollectionID)
	require.True(t, addressesEqual(receipt.Payer, decoded.Payer))
	require.True(t, addressesEqual(receipt.DataService, decoded.DataService))
	require.True(t, addressesEqual(receipt.ServiceProvider, decoded.ServiceProvider))
	require.Equal(t, receipt.TimestampNs, decoded.TimestampNs)
	require.Equal(t, receipt.Nonce, decoded.Nonce)
	require.Equal(t, 0, receipt.Value.Cmp(decoded.Value))
}

func TestRAV_JSON(t *testing.T) {
	var collectionID CollectionID
	rav := &RAV{
		CollectionID:    collectionID,
		Payer:           eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		TimestampNs:     1234567890,
		ValueAggregate:  big.NewInt(5000),
		Metadata:        []byte{1, 2, 3},
	}

	// Marshal
	data, err := json.Marshal(rav)
	require.NoError(t, err)

	// Unmarshal
	var decoded RAV
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	require.Equal(t, rav.CollectionID, decoded.CollectionID)
	require.True(t, addressesEqual(rav.Payer, decoded.Payer))
	require.True(t, addressesEqual(rav.ServiceProvider, decoded.ServiceProvider))
	require.True(t, addressesEqual(rav.DataService, decoded.DataService))
	require.Equal(t, rav.TimestampNs, decoded.TimestampNs)
	require.Equal(t, 0, rav.ValueAggregate.Cmp(decoded.ValueAggregate))
	require.Equal(t, rav.Metadata, decoded.Metadata)
}

func TestMaxUint128(t *testing.T) {
	// Check that MaxUint128 is 2^128 - 1
	expected := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
	require.Equal(t, 0, MaxUint128.Cmp(expected))

	// Check it has 128 bits
	require.Equal(t, 128, MaxUint128.BitLen())
}
