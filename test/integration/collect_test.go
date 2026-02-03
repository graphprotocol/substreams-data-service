package integration

import (
	"math/big"
	"testing"
	"time"

	"github.com/graphprotocol/substreams-data-service/horizon"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestCollectRAV tests the full collect() flow with escrow
func TestCollectRAV(t *testing.T) {
	env := SetupEnv(t)
	zlog.Info("starting TestCollectRAV", zap.Uint64("chain_id", env.ChainID))

	// Setup escrow, provision, register, and authorize signer
	setup := SetupTestWithSigner(t, env, nil)
	signerKey := setup.SignerKey
	signerAddr := setup.SignerAddr

	// Create domain and RAV
	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	collectionID := mustNewCollectionID("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")
	valueAggregate := big.NewInt(1000000000000000000) // 1 GRT

	rav := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           env.Payer.Address,
		ServiceProvider: env.ServiceProvider.Address,
		DataService:     env.DataService.Address,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  valueAggregate,
		Metadata:        []byte{},
	}

	// Sign RAV with authorized signer key
	signedRAV, err := horizon.Sign(domain, rav, signerKey)
	require.NoError(t, err)

	// Verify signature locally first
	recoveredSigner, err := signedRAV.RecoverSigner(domain)
	require.NoError(t, err)
	require.Equal(t, signerAddr, recoveredSigner)
	zlog.Debug("signature verified locally", zap.Stringer("recovered_signer", recoveredSigner))

	// Call collect() via SubstreamsDataService
	dataServiceCut := uint64(100000) // 10% in PPM
	zlog.Info("calling SubstreamsDataService.collect() on chain", zap.String("data_service", env.DataService.Address.Pretty()), zap.Uint64("chain_id", env.ChainID))
	tokensCollected, err := callDataServiceCollect(env, signedRAV, dataServiceCut)
	require.NoError(t, err)
	require.Equal(t, valueAggregate.Uint64(), tokensCollected)
	zlog.Info("SubstreamsDataService.collect() succeeded", zap.Uint64("tokens_collected", tokensCollected))

	// Verify tokensCollected mapping updated
	collected, err := callTokensCollected(env, env.DataService.Address, collectionID, env.ServiceProvider.Address, env.Payer.Address)
	require.NoError(t, err)
	require.Equal(t, valueAggregate.Uint64(), collected)

	t.Logf("Successfully collected %s tokens", valueAggregate.String())
}

// TestCollectRAVIncremental tests incremental RAV collection
func TestCollectRAVIncremental(t *testing.T) {
	env := SetupEnv(t)

	// Setup escrow, provision, register, and authorize signer
	setup := SetupTestWithSigner(t, env, nil)
	signerKey := setup.SignerKey

	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	collectionID := mustNewCollectionID("0xfedcba0987654321fedcba0987654321fedcba0987654321fedcba0987654321")

	// First RAV: 1 GRT
	rav1 := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           env.Payer.Address,
		ServiceProvider: env.ServiceProvider.Address,
		DataService:     env.DataService.Address,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  big.NewInt(1000000000000000000), // 1 GRT
		Metadata:        []byte{},
	}

	signedRAV1, err := horizon.Sign(domain, rav1, signerKey)
	require.NoError(t, err)

	dataServiceCut := uint64(100000) // 10% in PPM
	collected1, err := callDataServiceCollect(env, signedRAV1, dataServiceCut)
	require.NoError(t, err)
	require.Equal(t, uint64(1000000000000000000), collected1)

	// Second RAV: 3 GRT total (should collect 2 GRT delta)
	rav2 := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           env.Payer.Address,
		ServiceProvider: env.ServiceProvider.Address,
		DataService:     env.DataService.Address,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  big.NewInt(3000000000000000000), // 3 GRT
		Metadata:        []byte{},
	}

	signedRAV2, err := horizon.Sign(domain, rav2, signerKey)
	require.NoError(t, err)

	collected2, err := callDataServiceCollect(env, signedRAV2, dataServiceCut)
	require.NoError(t, err)
	require.Equal(t, uint64(2000000000000000000), collected2) // Delta: 2 GRT

	// Verify total tokensCollected is 3 GRT
	totalCollected, err := callTokensCollected(env, env.DataService.Address, collectionID, env.ServiceProvider.Address, env.Payer.Address)
	require.NoError(t, err)
	require.Equal(t, uint64(3000000000000000000), totalCollected)

	t.Logf("Successfully collected incrementally: first=%d, second=%d, total=%d",
		collected1, collected2, totalCollected)
}
