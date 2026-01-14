package horizon

import (
	"math/big"
	"testing"

	"github.com/streamingfast/eth-go"
	"github.com/stretchr/testify/require"
)

func TestSign_Receipt(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := NewDomain(chainID, verifyingContract)

	// Generate key
	key, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	var collectionID CollectionID
	receipt := &Receipt{
		CollectionID:    collectionID,
		Payer:           key.PublicKey().Address(),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     1234567890,
		Nonce:           999,
		Value:           big.NewInt(1000),
	}

	// Sign
	signed, err := Sign(domain, receipt, key)
	require.NoError(t, err)
	require.NotNil(t, signed)
	require.Equal(t, receipt, signed.Message)
	require.Equal(t, 65, len(signed.Signature))
}

func TestRecoverSigner_Receipt(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := NewDomain(chainID, verifyingContract)

	// Generate key
	key, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	expectedSigner := key.PublicKey().Address()

	var collectionID CollectionID
	receipt := &Receipt{
		CollectionID:    collectionID,
		Payer:           expectedSigner,
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     1234567890,
		Nonce:           999,
		Value:           big.NewInt(1000),
	}

	// Sign
	signed, err := Sign(domain, receipt, key)
	require.NoError(t, err)

	// Recover
	recoveredSigner, err := signed.RecoverSigner(domain)
	require.NoError(t, err)
	require.True(t, addressesEqual(expectedSigner, recoveredSigner))
}

func TestRecoverSigner_RAV(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := NewDomain(chainID, verifyingContract)

	// Generate key
	key, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	expectedSigner := key.PublicKey().Address()

	var collectionID CollectionID
	rav := &RAV{
		CollectionID:    collectionID,
		Payer:           expectedSigner,
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		TimestampNs:     1234567890,
		ValueAggregate:  big.NewInt(5000),
		Metadata:        []byte{},
	}

	// Sign
	signed, err := Sign(domain, rav, key)
	require.NoError(t, err)

	// Recover
	recoveredSigner, err := signed.RecoverSigner(domain)
	require.NoError(t, err)
	require.True(t, addressesEqual(expectedSigner, recoveredSigner))
}

func TestNormalizeSignature(t *testing.T) {
	// Create a signature with high-S value
	var highSSig eth.Signature

	// R (can be anything)
	r := big.NewInt(12345)
	rBytes := r.Bytes()
	copy(highSSig[32-len(rBytes):32], rBytes)

	// S (high value > N/2)
	// Use a value slightly higher than N/2
	s := new(big.Int).Add(secp256k1HalfN, big.NewInt(100))
	sBytes := s.Bytes()
	copy(highSSig[64-len(sBytes):64], sBytes)

	// V (recovery ID)
	highSSig[64] = 0

	// Normalize
	normalized := normalizeSignature(highSSig)

	// S should be flipped to N-S
	expectedS := new(big.Int).Sub(secp256k1N, s)
	normalizedS := new(big.Int).SetBytes(normalized[32:64])
	require.Equal(t, 0, expectedS.Cmp(normalizedS))

	// V should be flipped
	require.Equal(t, byte(1), normalized[64])

	// R should remain the same
	require.Equal(t, highSSig[:32], normalized[:32])
}

func TestSignaturesEqual(t *testing.T) {
	// Create two signatures that are equivalent but one has high-S
	var sig1, sig2 eth.Signature

	// Same R
	r := big.NewInt(12345)
	rBytes := r.Bytes()
	copy(sig1[32-len(rBytes):32], rBytes)
	copy(sig2[32-len(rBytes):32], rBytes)

	// S values that are complements (high and low form of same signature)
	s := new(big.Int).Add(secp256k1HalfN, big.NewInt(100))
	sBytes := s.Bytes()
	copy(sig1[64-len(sBytes):64], sBytes)

	// sig2 has the normalized (low-S) form
	sLow := new(big.Int).Sub(secp256k1N, s)
	sLowBytes := sLow.Bytes()
	copy(sig2[64-len(sLowBytes):64], sLowBytes)

	// V values are flipped
	sig1[64] = 0
	sig2[64] = 1

	// They should be considered equal
	require.True(t, SignaturesEqual(sig1, sig2))
}

func TestUniqueID(t *testing.T) {
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := NewDomain(chainID, verifyingContract)

	key, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	var collectionID CollectionID
	receipt := &Receipt{
		CollectionID:    collectionID,
		Payer:           key.PublicKey().Address(),
		DataService:     eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		ServiceProvider: eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		TimestampNs:     1234567890,
		Nonce:           999,
		Value:           big.NewInt(1000),
	}

	signed, err := Sign(domain, receipt, key)
	require.NoError(t, err)

	// Get unique ID
	uniqueID := signed.UniqueID()
	require.Equal(t, 65, len(uniqueID))

	// Should be deterministic
	uniqueID2 := signed.UniqueID()
	require.Equal(t, uniqueID, uniqueID2)

	// Should be normalized (low-S form)
	normalized := normalizeSignature(signed.Signature)
	require.Equal(t, normalized, uniqueID)
}
