package integration

import (
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/streamingfast/eth-go"
	horizon "github.com/streamingfast/horizon-go"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestAuthorizeSignerFlow tests the complete authorization flow
func TestAuthorizeSignerFlow(t *testing.T) {
	env := SetupEnv(t)
	zlog.Info("starting TestAuthorizeSignerFlow", zap.Uint64("chain_id", env.ChainID))

	// Setup escrow and provision
	tokensToDeposit := new(big.Int)
	tokensToDeposit.SetString("10000000000000000000000", 10) // 10,000 GRT

	err := callMintGRT(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.GRTToken, env.PayerAddr, tokensToDeposit, env.ABIs.GRTToken)
	require.NoError(t, err, "Failed to mint GRT")

	err = callApproveGRT(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.GRTToken, env.PaymentsEscrow, tokensToDeposit, env.ABIs.GRTToken)
	require.NoError(t, err, "Failed to approve GRT")

	err = callDepositEscrow(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.PaymentsEscrow, env.CollectorAddress, env.ServiceProviderAddr, tokensToDeposit, env.ABIs.Escrow)
	require.NoError(t, err, "Failed to deposit to escrow")

	// Set up SubstreamsDataService: set provision tokens range (min = 0 for testing)
	err = callSetProvisionTokensRange(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.SubstreamsDataService, big.NewInt(0), env.ABIs.DataService)
	require.NoError(t, err, "Failed to set provision tokens range")

	provisionTokens := new(big.Int)
	provisionTokens.SetString("1000000000000000000000", 10) // 1,000 GRT
	maxVerifierCut := uint32(0)
	thawingPeriod := uint64(0)
	err = callSetProvision(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.Staking, env.ServiceProviderAddr, env.SubstreamsDataService, provisionTokens, maxVerifierCut, thawingPeriod, env.ABIs.Staking)
	require.NoError(t, err, "Failed to set provision")

	// Register service provider with SubstreamsDataService (using ServiceProviderKey as operator)
	err = callRegisterWithDataService(env.ctx, env.rpcURL, env.ServiceProviderKey, env.ChainID, env.SubstreamsDataService, env.ServiceProviderAddr, env.ServiceProviderAddr, env.ABIs.DataService)
	require.NoError(t, err, "Failed to register with data service")

	// Create a signer key (different from payer)
	zlog.Debug("creating signer key")
	signerKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	signerAddr := signerKey.PublicKey().Address()
	zlog.Debug("signer key created", zap.Stringer("signer_address", signerAddr))

	// Verify signer is not authorized initially
	zlog.Debug("checking initial authorization status")
	isAuth, err := env.CallIsAuthorized(env.PayerAddr, signerAddr)
	require.NoError(t, err)
	require.False(t, isAuth, "Signer should not be authorized initially")
	zlog.Debug("verified signer not initially authorized")

	// Authorize the signer (payer authorizes signer) - requires signer's key to generate proof
	zlog.Info("authorizing signer", zap.Stringer("payer", env.PayerAddr), zap.Stringer("signer", signerAddr), zap.Uint64("chain_id", env.ChainID))
	err = callAuthorizeSigner(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.CollectorAddress, signerKey, env.ABIs.Collector)
	require.NoError(t, err, "Failed to authorize signer")
	zlog.Info("signer authorized successfully")

	// Verify signer is now authorized
	zlog.Debug("checking authorization status after authorization")
	isAuth, err = env.CallIsAuthorized(env.PayerAddr, signerAddr)
	require.NoError(t, err)
	require.True(t, isAuth, "Signer should be authorized after authorizeSigner")
	zlog.Debug("verified signer is now authorized")

	// Create and sign RAV with the authorized signer
	zlog.Debug("creating EIP-712 domain", zap.Uint64("chain_id", env.ChainID))
	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")[:])

	rav := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           env.PayerAddr, // Payer is different from signer
		ServiceProvider: env.ServiceProviderAddr,
		DataService:     env.SubstreamsDataService,
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
	tokensCollected, err := callDataServiceCollect(env.ctx, env.rpcURL, env.ServiceProviderKey, env.ChainID, env.SubstreamsDataService, env.ServiceProviderAddr, signedRAV, dataServiceCut, env)
	require.NoError(t, err)
	require.Equal(t, uint64(1000000000000000000), tokensCollected)
	zlog.Info("SubstreamsDataService.collect() with authorized signer succeeded")

	t.Logf("Successfully collected RAV signed by authorized signer")
}

// TestUnauthorizedSignerFails tests that collection fails with unauthorized signer
func TestUnauthorizedSignerFails(t *testing.T) {
	env := SetupEnv(t)
	zlog.Info("starting TestUnauthorizedSignerFails", zap.Uint64("chain_id", env.ChainID))

	// Setup escrow and provision
	tokensToDeposit := new(big.Int)
	tokensToDeposit.SetString("10000000000000000000000", 10) // 10,000 GRT

	err := callMintGRT(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.GRTToken, env.PayerAddr, tokensToDeposit, env.ABIs.GRTToken)
	require.NoError(t, err, "Failed to mint GRT")

	err = callApproveGRT(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.GRTToken, env.PaymentsEscrow, tokensToDeposit, env.ABIs.GRTToken)
	require.NoError(t, err, "Failed to approve GRT")

	err = callDepositEscrow(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.PaymentsEscrow, env.CollectorAddress, env.ServiceProviderAddr, tokensToDeposit, env.ABIs.Escrow)
	require.NoError(t, err, "Failed to deposit to escrow")

	// Set up SubstreamsDataService: set provision tokens range (min = 0 for testing)
	err = callSetProvisionTokensRange(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.SubstreamsDataService, big.NewInt(0), env.ABIs.DataService)
	require.NoError(t, err, "Failed to set provision tokens range")

	provisionTokens := new(big.Int)
	provisionTokens.SetString("1000000000000000000000", 10) // 1,000 GRT
	maxVerifierCut := uint32(0)
	thawingPeriod := uint64(0)
	err = callSetProvision(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.Staking, env.ServiceProviderAddr, env.SubstreamsDataService, provisionTokens, maxVerifierCut, thawingPeriod, env.ABIs.Staking)
	require.NoError(t, err, "Failed to set provision")

	// Register service provider with SubstreamsDataService
	err = callRegisterWithDataService(env.ctx, env.rpcURL, env.ServiceProviderKey, env.ChainID, env.SubstreamsDataService, env.ServiceProviderAddr, env.ServiceProviderAddr, env.ABIs.DataService)
	require.NoError(t, err, "Failed to register with data service")

	// Create an unauthorized signer key
	zlog.Debug("creating unauthorized signer key")
	unauthorizedKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	unauthorizedAddr := unauthorizedKey.PublicKey().Address()
	zlog.Debug("unauthorized signer created", zap.Stringer("unauthorized_address", unauthorizedAddr))

	// Verify signer is not authorized
	zlog.Debug("verifying signer is not authorized")
	isAuth, err := env.CallIsAuthorized(env.PayerAddr, unauthorizedAddr)
	require.NoError(t, err)
	require.False(t, isAuth)
	zlog.Debug("confirmed signer is not authorized")

	// Create and sign RAV with unauthorized signer
	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")[:])

	rav := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           env.PayerAddr,
		ServiceProvider: env.ServiceProviderAddr,
		DataService:     env.SubstreamsDataService,
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
	_, err = callDataServiceCollect(env.ctx, env.rpcURL, env.ServiceProviderKey, env.ChainID, env.SubstreamsDataService, env.ServiceProviderAddr, signedRAV, dataServiceCut, env)
	require.Error(t, err, "Collection should fail with unauthorized signer")
	zlog.Info("SubstreamsDataService.collect() correctly failed with unauthorized signer", zap.Error(err))

	t.Logf("Collection correctly failed with unauthorized signer")
}

// TestRevokeSignerFlow tests the revoke signer flow (without thawing period)
func TestRevokeSignerFlow(t *testing.T) {
	env := SetupEnv(t)
	zlog.Info("starting TestRevokeSignerFlow", zap.Uint64("chain_id", env.ChainID))

	// Setup escrow and provision
	tokensToDeposit := new(big.Int)
	tokensToDeposit.SetString("10000000000000000000000", 10) // 10,000 GRT

	err := callMintGRT(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.GRTToken, env.PayerAddr, tokensToDeposit, env.ABIs.GRTToken)
	require.NoError(t, err, "Failed to mint GRT")

	err = callApproveGRT(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.GRTToken, env.PaymentsEscrow, tokensToDeposit, env.ABIs.GRTToken)
	require.NoError(t, err, "Failed to approve GRT")

	err = callDepositEscrow(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.PaymentsEscrow, env.CollectorAddress, env.ServiceProviderAddr, tokensToDeposit, env.ABIs.Escrow)
	require.NoError(t, err, "Failed to deposit to escrow")

	// Set up SubstreamsDataService: set provision tokens range (min = 0 for testing)
	err = callSetProvisionTokensRange(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.SubstreamsDataService, big.NewInt(0), env.ABIs.DataService)
	require.NoError(t, err, "Failed to set provision tokens range")

	provisionTokens := new(big.Int)
	provisionTokens.SetString("1000000000000000000000", 10) // 1,000 GRT
	maxVerifierCut := uint32(0)
	thawingPeriod := uint64(0)
	err = callSetProvision(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID, env.Staking, env.ServiceProviderAddr, env.SubstreamsDataService, provisionTokens, maxVerifierCut, thawingPeriod, env.ABIs.Staking)
	require.NoError(t, err, "Failed to set provision")

	// Register service provider with SubstreamsDataService
	err = callRegisterWithDataService(env.ctx, env.rpcURL, env.ServiceProviderKey, env.ChainID, env.SubstreamsDataService, env.ServiceProviderAddr, env.ServiceProviderAddr, env.ABIs.DataService)
	require.NoError(t, err, "Failed to register with data service")

	// Create a signer key
	signerKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	signerAddr := signerKey.PublicKey().Address()

	// Authorize the signer - requires signer's key to generate proof
	err = callAuthorizeSigner(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.CollectorAddress, signerKey, env.ABIs.Collector)
	require.NoError(t, err, "Failed to authorize signer")

	// Verify signer is authorized
	isAuth, err := env.CallIsAuthorized(env.PayerAddr, signerAddr)
	require.NoError(t, err)
	require.True(t, isAuth)

	// Revoke the signer (thawing period is 0 in our setup, so can revoke immediately)
	zlog.Info("revoking signer", zap.Stringer("signer", signerAddr), zap.Uint64("chain_id", env.ChainID))
	err = callRevokeSigner(env.ctx, env.rpcURL, env.PayerKey, env.ChainID, env.CollectorAddress, signerAddr, env.ABIs.Collector)
	require.NoError(t, err, "Failed to revoke signer")
	zlog.Info("signer revoked successfully")

	// Verify signer is no longer authorized
	zlog.Debug("verifying signer is no longer authorized")
	isAuth, err = env.CallIsAuthorized(env.PayerAddr, signerAddr)
	require.NoError(t, err)
	require.False(t, isAuth, "Signer should not be authorized after revoke")
	zlog.Debug("confirmed signer is no longer authorized")

	// Try to collect with revoked signer - should fail
	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc")[:])

	rav := &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           env.PayerAddr,
		ServiceProvider: env.ServiceProviderAddr,
		DataService:     env.SubstreamsDataService,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  big.NewInt(1000000000000000000), // 1 GRT
		Metadata:        []byte{},
	}

	signedRAV, err := horizon.Sign(domain, rav, signerKey)
	require.NoError(t, err)

	dataServiceCut := uint64(100000)
	zlog.Info("calling SubstreamsDataService.collect() with revoked signer (expecting failure)", zap.Uint64("chain_id", env.ChainID))
	_, err = callDataServiceCollect(env.ctx, env.rpcURL, env.ServiceProviderKey, env.ChainID, env.SubstreamsDataService, env.ServiceProviderAddr, signedRAV, dataServiceCut, env)
	require.Error(t, err, "Collection should fail with revoked signer")
	zlog.Info("SubstreamsDataService.collect() correctly failed with revoked signer", zap.Error(err))

	t.Logf("Collection correctly failed with revoked signer")
}

// ========== Contract Call Helpers ==========

// callAuthorizeSigner calls Authorizable.authorizeSigner(address signer, uint256 proofDeadline, bytes proof)
// This is the ORIGINAL contract signature that requires a cryptographic proof from the signer
func callAuthorizeSigner(ctx testContext, rpcURL string, authorizerKey *eth.PrivateKey, chainID uint64, collector eth.Address, signerKey *eth.PrivateKey, abi *eth.ABI) error {
	authorizerAddr := authorizerKey.PublicKey().Address()
	signerAddr := signerKey.PublicKey().Address()

	// Generate proof with deadline 1 hour in the future
	proofDeadline := uint64(time.Now().Add(1 * time.Hour).Unix())

	proof, err := GenerateSignerProof(chainID, collector, proofDeadline, authorizerAddr, signerKey)
	if err != nil {
		return fmt.Errorf("generating signer proof: %w", err)
	}

	authorizeSignerFn := abi.FindFunctionByName("authorizeSigner")
	if authorizeSignerFn == nil {
		return fmt.Errorf("authorizeSigner function not found in ABI")
	}

	// Encode call: authorizeSigner(address signer, uint256 proofDeadline, bytes proof)
	data, err := authorizeSignerFn.NewCall(signerAddr, new(big.Int).SetUint64(proofDeadline), proof).Encode()
	if err != nil {
		return fmt.Errorf("encoding authorizeSigner call: %w", err)
	}

	return sendTransaction(ctx, rpcURL, authorizerKey, chainID, &collector, big.NewInt(0), data)
}

// callThawSigner calls Authorizable.thawSigner(address signer)
// This starts the thawing process before revocation
func callThawSigner(ctx testContext, rpcURL string, key *eth.PrivateKey, chainID uint64, collector eth.Address, signer eth.Address, abi *eth.ABI) error {
	thawSignerFn := abi.FindFunctionByName("thawSigner")
	if thawSignerFn == nil {
		return fmt.Errorf("thawSigner function not found in ABI")
	}

	data, err := thawSignerFn.NewCall(signer).Encode()
	if err != nil {
		return fmt.Errorf("encoding thawSigner call: %w", err)
	}

	return sendTransaction(ctx, rpcURL, key, chainID, &collector, big.NewInt(0), data)
}

// callRevokeAuthorizedSigner calls Authorizable.revokeAuthorizedSigner(address signer)
// This completes the revocation after thawing period has passed
func callRevokeAuthorizedSigner(ctx testContext, rpcURL string, key *eth.PrivateKey, chainID uint64, collector eth.Address, signer eth.Address, abi *eth.ABI) error {
	revokeSignerFn := abi.FindFunctionByName("revokeAuthorizedSigner")
	if revokeSignerFn == nil {
		return fmt.Errorf("revokeAuthorizedSigner function not found in ABI")
	}

	data, err := revokeSignerFn.NewCall(signer).Encode()
	if err != nil {
		return fmt.Errorf("encoding revokeAuthorizedSigner call: %w", err)
	}

	return sendTransaction(ctx, rpcURL, key, chainID, &collector, big.NewInt(0), data)
}

// callRevokeSigner performs the two-step revoke flow: thaw + revoke
// Since thawing period is 0 in our test setup, we can call both immediately
func callRevokeSigner(ctx testContext, rpcURL string, key *eth.PrivateKey, chainID uint64, collector eth.Address, signer eth.Address, abi *eth.ABI) error {
	// Step 1: Thaw the signer
	if err := callThawSigner(ctx, rpcURL, key, chainID, collector, signer, abi); err != nil {
		return fmt.Errorf("thawing signer: %w", err)
	}

	// Step 2: Revoke the signer (thawing period is 0, so we can do this immediately)
	if err := callRevokeAuthorizedSigner(ctx, rpcURL, key, chainID, collector, signer, abi); err != nil {
		return fmt.Errorf("revoking signer: %w", err)
	}

	return nil
}

// CallIsAuthorized queries Authorizable.isAuthorized(address authorizer, address signer)
func (env *TestEnv) CallIsAuthorized(authorizer eth.Address, signer eth.Address) (bool, error) {
	isAuthorizedFn := env.ABIs.Collector.FindFunctionByName("isAuthorized")
	if isAuthorizedFn == nil {
		return false, fmt.Errorf("isAuthorized function not found in ABI")
	}

	data, err := isAuthorizedFn.NewCall(authorizer, signer).Encode()
	if err != nil {
		return false, fmt.Errorf("encoding isAuthorized call: %w", err)
	}

	result, err := env.CallContract(env.CollectorAddress, data)
	if err != nil {
		return false, err
	}

	// Result is bool (32 bytes, last byte is 0 or 1)
	return result[31] == 1, nil
}
