package integration

import (
	"math/big"
	"testing"
	"time"

	"github.com/graphprotocol/substreams-data-service/horizon"
	"github.com/streamingfast/eth-go"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestAuthorizeSignerFlow tests the complete authorization flow
func TestAuthorizeSignerFlow(t *testing.T) {
	env := SetupEnv(t)
	zlog.Info("starting TestAuthorizeSignerFlow", zap.Uint64("chain_id", env.ChainID))

	// Setup escrow, provision, and create a signer key (but don't authorize it yet - we test that below)
	config := DefaultTestSetupConfig()

	require.NoError(t, callMintGRT(env, env.Payer.Address, config.EscrowAmount), "Failed to mint GRT")
	require.NoError(t, callApproveGRT(env, config.EscrowAmount), "Failed to approve GRT")
	require.NoError(t, callDepositEscrow(env, config.EscrowAmount), "Failed to deposit to escrow")
	require.NoError(t, callSetProvisionTokensRange(env, big.NewInt(0)), "Failed to set provision tokens range")
	require.NoError(t, callSetProvision(env, config.ProvisionAmount, 0, 0), "Failed to set provision")
	require.NoError(t, callRegisterWithDataService(env), "Failed to register with data service")

	// Create a signer key (different from payer) - we'll authorize it manually for this test
	signerKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	signerAddr := signerKey.PublicKey().Address()
	zlog.Debug("signer key created", zap.Stringer("signer_address", signerAddr))

	// Verify signer is not authorized initially
	zlog.Debug("checking initial authorization status")
	isAuth, err := env.IsAuthorized(env.Payer.Address, signerAddr)
	require.NoError(t, err)
	require.False(t, isAuth, "Signer should not be authorized initially")
	zlog.Debug("verified signer not initially authorized")

	// Authorize the signer (payer authorizes signer) - requires signer's key to generate proof
	zlog.Info("authorizing signer", zap.Stringer("payer", env.Payer.Address), zap.Stringer("signer", signerAddr), zap.Uint64("chain_id", env.ChainID))
	err = callAuthorizeSigner(env, signerKey)
	require.NoError(t, err, "Failed to authorize signer")
	zlog.Info("signer authorized successfully")

	// Verify signer is now authorized
	zlog.Debug("checking authorization status after authorization")
	isAuth, err = env.IsAuthorized(env.Payer.Address, signerAddr)
	require.NoError(t, err)
	require.True(t, isAuth, "Signer should be authorized after authorizeSigner")
	zlog.Debug("verified signer is now authorized")

	// Create and sign RAV with the authorized signer
	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	collectionID := mustNewCollectionID("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	rav := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           env.Payer.Address,
		ServiceProvider: env.ServiceProvider.Address,
		DataService:     env.DataService.Address,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  big.NewInt(1000000000000000000), // 1 GRT
		Metadata:        []byte{},
	}

	// Sign with authorized signer (not the payer)
	zlog.Debug("signing RAV with authorized signer (not payer)")
	signedRAV, err := horizon.Sign(domain, rav, signerKey)
	require.NoError(t, err)
	zlog.Debug("RAV signed with authorized signer")

	// Verify the signer is recovered correctly
	recoveredSigner, err := signedRAV.RecoverSigner(domain)
	require.NoError(t, err)
	require.Equal(t, signerAddr, recoveredSigner, "Should recover signer address, not payer")
	zlog.Debug("verified signature recovery", zap.Stringer("recovered", recoveredSigner), zap.Stringer("expected", signerAddr))

	// Call collect() via SubstreamsDataService - should succeed because signer is authorized
	dataServiceCut := uint64(100000) // 10% in PPM
	zlog.Info("calling SubstreamsDataService.collect() with authorized signer", zap.Uint64("chain_id", env.ChainID))
	tokensCollected, err := callDataServiceCollect(env, signedRAV, dataServiceCut)
	require.NoError(t, err)
	require.Equal(t, uint64(1000000000000000000), tokensCollected)
	zlog.Info("SubstreamsDataService.collect() with authorized signer succeeded")

	t.Logf("Successfully collected RAV signed by authorized signer")
}

// TestUnauthorizedSignerFails tests that collection fails with unauthorized signer
func TestUnauthorizedSignerFails(t *testing.T) {
	env := SetupEnv(t)
	zlog.Info("starting TestUnauthorizedSignerFails", zap.Uint64("chain_id", env.ChainID))

	// Setup escrow and provision (but don't authorize a signer)
	config := DefaultTestSetupConfig()

	require.NoError(t, callMintGRT(env, env.Payer.Address, config.EscrowAmount), "Failed to mint GRT")
	require.NoError(t, callApproveGRT(env, config.EscrowAmount), "Failed to approve GRT")
	require.NoError(t, callDepositEscrow(env, config.EscrowAmount), "Failed to deposit to escrow")
	require.NoError(t, callSetProvisionTokensRange(env, big.NewInt(0)), "Failed to set provision tokens range")
	require.NoError(t, callSetProvision(env, config.ProvisionAmount, 0, 0), "Failed to set provision")
	require.NoError(t, callRegisterWithDataService(env), "Failed to register with data service")

	// Create an unauthorized signer key (intentionally not calling callAuthorizeSigner)
	unauthorizedKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	unauthorizedAddr := unauthorizedKey.PublicKey().Address()
	zlog.Debug("unauthorized signer created", zap.Stringer("unauthorized_address", unauthorizedAddr))

	// Verify signer is not authorized
	zlog.Debug("verifying signer is not authorized")
	isAuth, err := env.IsAuthorized(env.Payer.Address, unauthorizedAddr)
	require.NoError(t, err)
	require.False(t, isAuth)
	zlog.Debug("confirmed signer is not authorized")

	// Create and sign RAV with unauthorized signer
	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	collectionID := mustNewCollectionID("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	rav := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           env.Payer.Address,
		ServiceProvider: env.ServiceProvider.Address,
		DataService:     env.DataService.Address,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  big.NewInt(1000000000000000000), // 1 GRT
		Metadata:        []byte{},
	}

	// Sign with unauthorized signer
	signedRAV, err := horizon.Sign(domain, rav, unauthorizedKey)
	require.NoError(t, err)

	// Call collect() via SubstreamsDataService - should fail
	dataServiceCut := uint64(100000) // 10% in PPM
	zlog.Info("calling SubstreamsDataService.collect() with unauthorized signer (expecting failure)", zap.Uint64("chain_id", env.ChainID))
	_, err = callDataServiceCollect(env, signedRAV, dataServiceCut)
	require.Error(t, err, "Collection should fail with unauthorized signer")
	zlog.Info("SubstreamsDataService.collect() correctly failed with unauthorized signer", zap.Error(err))

	t.Logf("Collection correctly failed with unauthorized signer")
}

// TestRevokeSignerFlow tests the revoke signer flow (without thawing period)
func TestRevokeSignerFlow(t *testing.T) {
	env := SetupEnv(t)
	zlog.Info("starting TestRevokeSignerFlow", zap.Uint64("chain_id", env.ChainID))

	// Setup escrow, provision, register, and authorize signer
	setup := SetupTestWithSigner(t, env, nil)
	signerKey := setup.SignerKey
	signerAddr := setup.SignerAddr

	// Verify signer is authorized
	isAuth, err := env.IsAuthorized(env.Payer.Address, signerAddr)
	require.NoError(t, err)
	require.True(t, isAuth)

	// Revoke the signer (thawing period is 0 in our setup, so can revoke immediately)
	zlog.Info("revoking signer", zap.Stringer("signer", signerAddr), zap.Uint64("chain_id", env.ChainID))
	err = callRevokeSigner(env, signerAddr)
	require.NoError(t, err, "Failed to revoke signer")
	zlog.Info("signer revoked successfully")

	// Verify signer is no longer authorized
	zlog.Debug("verifying signer is no longer authorized")
	isAuth, err = env.IsAuthorized(env.Payer.Address, signerAddr)
	require.NoError(t, err)
	require.False(t, isAuth, "Signer should not be authorized after revoke")
	zlog.Debug("confirmed signer is no longer authorized")

	// Try to collect with revoked signer - should fail
	domain := horizon.NewDomain(env.ChainID, env.Collector.Address)
	collectionID := mustNewCollectionID("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")

	rav := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           env.Payer.Address,
		ServiceProvider: env.ServiceProvider.Address,
		DataService:     env.DataService.Address,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  big.NewInt(1000000000000000000), // 1 GRT
		Metadata:        []byte{},
	}

	signedRAV, err := horizon.Sign(domain, rav, signerKey)
	require.NoError(t, err)

	dataServiceCut := uint64(100000)
	zlog.Info("calling SubstreamsDataService.collect() with revoked signer (expecting failure)", zap.Uint64("chain_id", env.ChainID))
	_, err = callDataServiceCollect(env, signedRAV, dataServiceCut)
	require.Error(t, err, "Collection should fail with revoked signer")
	zlog.Info("SubstreamsDataService.collect() correctly failed with revoked signer", zap.Error(err))

	t.Logf("Collection correctly failed with revoked signer")
}
