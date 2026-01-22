package integration

import (
	"math/big"
	"testing"
	"time"

	"github.com/streamingfast/eth-go"
	horizon "github.com/streamingfast/horizon-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// This test implements the full Substreams Network Payments flow as described in the flow diagram.
// The flow involves:
//   - Substreams (ss): The consumer running a substreams query
//   - Sidecar (sc): The consumer's sidecar handling payment aggregation
//   - ProviderSidecar (psc): The provider's sidecar validating RAVs and tracking usage
//   - Provider (p): The data provider streaming block data
//
// Flow:
//   1. ss -> sc: init() - Consumer initializes session
//   2. sc -> psc: startSession(escrow_account, RAV0) - Start session with initial escrow info
//   3. psc -> sc: useThis(RAVx) - Provider confirms which RAV to use as starting point
//   4. sc -> ss: RAVx - Consumer receives the starting RAV
//   5. ss -> p: Blocks() with header payment=RAVx - Consumer requests data with payment info
//   6. p -> psc: validate RAVx - Provider validates the RAV
//   7. psc -> p: OK - Validation successful
//   8. p -> ss: sessionInit(required_blocks_preproc=N) - Provider confirms session params
//   9. Loop: p -> ss: data..., p -> psc: sent(usage), ss -> sc: received(usage)
//  10. psc -> sc: requestRAV(RAVx, usage) - Provider requests updated RAV
//  11. sc -> psc: signed(RAVx) - Consumer returns signed RAV
//  12. psc -> p: Continue() or Stop() - Provider decides to continue or stop
//  13. Optional loop: psc checks escrow funds on blockchain
//  14. p -> ss: end() - Session ends
//  15. Final RAV request and collection

// ========== Mock Structures for Flow Simulation ==========

// SubstreamsClient represents the consumer running substreams (ss)
type SubstreamsClient struct {
	name    string
	sidecar *ConsumerSidecar
}

// ConsumerSidecar represents the consumer's payment sidecar (sc)
type ConsumerSidecar struct {
	name            string
	domain          *horizon.Domain
	signerKey       *eth.PrivateKey
	signerAddr      eth.Address
	payerAddr       eth.Address
	serviceProvider eth.Address
	dataService     eth.Address
	currentRAV      *horizon.SignedRAV
	collectionID    horizon.CollectionID
	totalUsage      *big.Int
}

// ProviderSidecar represents the provider's validation sidecar (psc)
type ProviderSidecar struct {
	name               string
	domain             *horizon.Domain
	acceptedSigners    []eth.Address
	currentRAV         *horizon.SignedRAV
	trackedUsage       *big.Int
	collectionID       horizon.CollectionID
	escrowBalance      *big.Int
	env                *TestEnv
	payerAddr          eth.Address
	serviceProviderKey *eth.PrivateKey
}

// Provider represents the data provider (p)
type Provider struct {
	name               string
	sidecar            *ProviderSidecar
	dataServiceAddr    eth.Address
	serviceProviderKey *eth.PrivateKey
	sessionActive      bool
	blocksSent         uint64
	requiredPreproc    uint64
	collectorAddress   eth.Address
	dataServiceCut     uint64
	serviceProvider    eth.Address
}

// SessionRequest represents the startSession request from consumer to provider
type SessionRequest struct {
	EscrowAccount eth.Address
	InitialRAV    *horizon.SignedRAV
}

// SessionResponse represents the response from provider to consumer
type SessionResponse struct {
	AcceptedRAV           *horizon.SignedRAV
	RequiredBlocksPreproc uint64
}

// UsageReport represents usage tracking between sidecars
type UsageReport struct {
	BlocksProcessed uint64
	BytesSent       uint64
	Value           *big.Int
}

// RAVRequest represents a request for a new signed RAV
type RAVRequest struct {
	PreviousRAV *horizon.SignedRAV
	Usage       *UsageReport
}

// ========== Flow Implementation ==========

// NewSubstreamsClient creates a new Substreams consumer
func NewSubstreamsClient(name string, sidecar *ConsumerSidecar) *SubstreamsClient {
	return &SubstreamsClient{
		name:    name,
		sidecar: sidecar,
	}
}

// NewConsumerSidecar creates a new consumer sidecar for payment management
func NewConsumerSidecar(
	name string,
	domain *horizon.Domain,
	signerKey *eth.PrivateKey,
	payerAddr eth.Address,
	serviceProvider eth.Address,
	dataService eth.Address,
	collectionID horizon.CollectionID,
) *ConsumerSidecar {
	return &ConsumerSidecar{
		name:            name,
		domain:          domain,
		signerKey:       signerKey,
		signerAddr:      signerKey.PublicKey().Address(),
		payerAddr:       payerAddr,
		serviceProvider: serviceProvider,
		dataService:     dataService,
		collectionID:    collectionID,
		totalUsage:      big.NewInt(0),
	}
}

// NewProviderSidecar creates a new provider sidecar for RAV validation
func NewProviderSidecar(
	name string,
	domain *horizon.Domain,
	acceptedSigners []eth.Address,
	collectionID horizon.CollectionID,
	escrowBalance *big.Int,
	env *TestEnv,
	payerAddr eth.Address,
	serviceProviderKey *eth.PrivateKey,
) *ProviderSidecar {
	return &ProviderSidecar{
		name:               name,
		domain:             domain,
		acceptedSigners:    acceptedSigners,
		collectionID:       collectionID,
		trackedUsage:       big.NewInt(0),
		escrowBalance:      escrowBalance,
		env:                env,
		payerAddr:          payerAddr,
		serviceProviderKey: serviceProviderKey,
	}
}

// NewProvider creates a new data provider
func NewProvider(
	name string,
	sidecar *ProviderSidecar,
	dataServiceAddr eth.Address,
	serviceProviderKey *eth.PrivateKey,
	collectorAddress eth.Address,
	serviceProvider eth.Address,
) *Provider {
	return &Provider{
		name:               name,
		sidecar:            sidecar,
		dataServiceAddr:    dataServiceAddr,
		serviceProviderKey: serviceProviderKey,
		requiredPreproc:    1000, // Default blocks to preprocess
		collectorAddress:   collectorAddress,
		dataServiceCut:     100000, // 10% in PPM
		serviceProvider:    serviceProvider,
	}
}

// Init initializes a substreams session (ss -> sc)
func (ss *SubstreamsClient) Init() error {
	zlog.Debug("Substreams: init()", zap.String("client", ss.name))
	return nil
}

// StartSession starts a payment session with the provider (sc -> psc)
func (sc *ConsumerSidecar) StartSession(psc *ProviderSidecar, escrowAccount eth.Address) (*SessionResponse, error) {
	zlog.Debug("ConsumerSidecar: startSession()",
		zap.String("sidecar", sc.name),
		zap.Stringer("escrow_account", escrowAccount))

	// Create initial RAV (RAV0) with zero value
	// Note: The flow diagram asks "can we emit a 0-value RAV?" - yes, we can
	rav0 := &horizon.RAV{
		CollectionID:    sc.collectionID,
		Payer:           sc.payerAddr,
		ServiceProvider: sc.serviceProvider,
		DataService:     sc.dataService,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  big.NewInt(0),
		Metadata:        []byte{},
	}

	signedRAV0, err := horizon.Sign(sc.domain, rav0, sc.signerKey)
	if err != nil {
		return nil, err
	}

	// Send to provider sidecar
	req := &SessionRequest{
		EscrowAccount: escrowAccount,
		InitialRAV:    signedRAV0,
	}

	return psc.HandleStartSession(req)
}

// HandleStartSession handles the startSession request (psc receives)
func (psc *ProviderSidecar) HandleStartSession(req *SessionRequest) (*SessionResponse, error) {
	zlog.Debug("ProviderSidecar: handling startSession()",
		zap.String("sidecar", psc.name),
		zap.Stringer("escrow_account", req.EscrowAccount))

	// First escrow funds validation here (including check for existing RAVs)
	// In a real implementation, this would check the blockchain
	if psc.escrowBalance.Cmp(big.NewInt(0)) <= 0 {
		return nil, horizon.ErrNoReceipts // Using existing error for simplicity
	}

	// Validate the initial RAV signature
	if req.InitialRAV != nil {
		signer, err := req.InitialRAV.RecoverSigner(psc.domain)
		if err != nil {
			return nil, err
		}

		// Check if signer is authorized
		authorized := false
		for _, accepted := range psc.acceptedSigners {
			if signer.Pretty() == accepted.Pretty() {
				authorized = true
				break
			}
		}
		if !authorized {
			return nil, horizon.ErrInvalidSigner
		}
	}

	// psc -> sc: useThis(RAVx) - either the same or another RAV
	// For this test, we accept the initial RAV
	psc.currentRAV = req.InitialRAV

	return &SessionResponse{
		AcceptedRAV:           req.InitialRAV,
		RequiredBlocksPreproc: 1000,
	}, nil
}

// ReceiveSessionResponse receives the session response (sc receives from psc)
func (sc *ConsumerSidecar) ReceiveSessionResponse(resp *SessionResponse) {
	zlog.Debug("ConsumerSidecar: received session response",
		zap.String("sidecar", sc.name),
		zap.Uint64("required_blocks_preproc", resp.RequiredBlocksPreproc))

	sc.currentRAV = resp.AcceptedRAV
}

// RequestBlocks starts requesting blocks from provider (ss -> p)
func (ss *SubstreamsClient) RequestBlocks(p *Provider) (*SessionResponse, error) {
	zlog.Debug("Substreams: Blocks() request",
		zap.String("client", ss.name),
		zap.String("provider", p.name))

	// Include payment info in header
	currentRAV := ss.sidecar.currentRAV

	return p.HandleBlocksRequest(currentRAV)
}

// HandleBlocksRequest handles incoming Blocks() request (p receives)
func (p *Provider) HandleBlocksRequest(paymentRAV *horizon.SignedRAV) (*SessionResponse, error) {
	zlog.Debug("Provider: handling Blocks() request",
		zap.String("provider", p.name))

	// p -> psc: validate RAVx
	if err := p.sidecar.ValidateRAV(paymentRAV); err != nil {
		return nil, err
	}

	// psc -> p: OK
	p.sessionActive = true

	// p -> ss: sessionInit(required_blocks_preproc=N)
	return &SessionResponse{
		AcceptedRAV:           paymentRAV,
		RequiredBlocksPreproc: p.requiredPreproc,
	}, nil
}

// ValidateRAV validates a RAV from consumer (psc validates)
func (psc *ProviderSidecar) ValidateRAV(signedRAV *horizon.SignedRAV) error {
	zlog.Debug("ProviderSidecar: validating RAV",
		zap.String("sidecar", psc.name))

	if signedRAV == nil {
		return nil // No RAV to validate (first request)
	}

	// Recover and verify signer
	signer, err := signedRAV.RecoverSigner(psc.domain)
	if err != nil {
		return err
	}

	// Check if signer is authorized
	authorized := false
	for _, accepted := range psc.acceptedSigners {
		if signer.Pretty() == accepted.Pretty() {
			authorized = true
			break
		}
	}
	if !authorized {
		return horizon.ErrInvalidSigner
	}

	// Validate collection ID matches
	if signedRAV.Message.CollectionID != psc.collectionID {
		return horizon.ErrCollectionMismatch
	}

	return nil
}

// SendData sends block data to consumer and reports usage (p -> ss, p -> psc)
func (p *Provider) SendData(blocksToSend uint64, valuePerBlock *big.Int) *UsageReport {
	zlog.Debug("Provider: sending data",
		zap.String("provider", p.name),
		zap.Uint64("blocks", blocksToSend))

	p.blocksSent += blocksToSend

	usage := &UsageReport{
		BlocksProcessed: blocksToSend,
		BytesSent:       blocksToSend * 1024, // Assume 1KB per block
		Value:           new(big.Int).Mul(valuePerBlock, big.NewInt(int64(blocksToSend))),
	}

	// p -> psc: sent(usage)
	p.sidecar.TrackSentUsage(usage)

	return usage
}

// TrackSentUsage tracks usage reported by provider (psc tracks)
func (psc *ProviderSidecar) TrackSentUsage(usage *UsageReport) {
	zlog.Debug("ProviderSidecar: tracking sent usage",
		zap.String("sidecar", psc.name),
		zap.Uint64("blocks", usage.BlocksProcessed),
		zap.String("value", usage.Value.String()))

	psc.trackedUsage.Add(psc.trackedUsage, usage.Value)
}

// ReceiveData receives data and reports to sidecar (ss receives, ss -> sc)
func (ss *SubstreamsClient) ReceiveData(usage *UsageReport) {
	zlog.Debug("Substreams: received data",
		zap.String("client", ss.name),
		zap.Uint64("blocks", usage.BlocksProcessed))

	// ss -> sc: received(usage)
	ss.sidecar.TrackReceivedUsage(usage)
}

// TrackReceivedUsage tracks usage received from substreams (sc tracks)
func (sc *ConsumerSidecar) TrackReceivedUsage(usage *UsageReport) {
	zlog.Debug("ConsumerSidecar: tracking received usage",
		zap.String("sidecar", sc.name),
		zap.Uint64("blocks", usage.BlocksProcessed),
		zap.String("value", usage.Value.String()))

	sc.totalUsage.Add(sc.totalUsage, usage.Value)
}

// RequestRAV requests a new signed RAV from consumer (psc -> sc)
func (psc *ProviderSidecar) RequestRAV(sc *ConsumerSidecar) (*horizon.SignedRAV, error) {
	zlog.Debug("ProviderSidecar: requesting RAV",
		zap.String("provider_sidecar", psc.name),
		zap.String("consumer_sidecar", sc.name),
		zap.String("tracked_usage", psc.trackedUsage.String()))

	req := &RAVRequest{
		PreviousRAV: psc.currentRAV,
		Usage: &UsageReport{
			Value: psc.trackedUsage,
		},
	}

	return sc.HandleRAVRequest(req)
}

// HandleRAVRequest handles RAV request from provider (sc handles)
func (sc *ConsumerSidecar) HandleRAVRequest(req *RAVRequest) (*horizon.SignedRAV, error) {
	zlog.Debug("ConsumerSidecar: handling RAV request",
		zap.String("sidecar", sc.name),
		zap.String("requested_value", req.Usage.Value.String()))

	// Create new RAV with updated value
	rav := &horizon.RAV{
		CollectionID:    sc.collectionID,
		Payer:           sc.payerAddr,
		ServiceProvider: sc.serviceProvider,
		DataService:     sc.dataService,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  new(big.Int).Set(req.Usage.Value),
		Metadata:        []byte{},
	}

	// Sign the RAV
	signedRAV, err := horizon.Sign(sc.domain, rav, sc.signerKey)
	if err != nil {
		return nil, err
	}

	sc.currentRAV = signedRAV
	return signedRAV, nil
}

// ReceiveSignedRAV receives the signed RAV from consumer (psc receives)
func (psc *ProviderSidecar) ReceiveSignedRAV(signedRAV *horizon.SignedRAV) error {
	zlog.Debug("ProviderSidecar: received signed RAV",
		zap.String("sidecar", psc.name),
		zap.String("value", signedRAV.Message.ValueAggregate.String()))

	// Validate the new RAV
	if err := psc.ValidateRAV(signedRAV); err != nil {
		return err
	}

	psc.currentRAV = signedRAV
	return nil
}

// CheckEscrowFunds checks escrow funds on blockchain (psc -> blockchain)
func (psc *ProviderSidecar) CheckEscrowFunds() (*big.Int, error) {
	zlog.Debug("ProviderSidecar: checking escrow funds",
		zap.String("sidecar", psc.name))

	// In a real implementation, this would call the blockchain
	// For testing, we use the stored balance
	return psc.escrowBalance, nil
}

// CheckSumOfAllKnownRAVs checks total RAV values (psc internal check)
func (psc *ProviderSidecar) CheckSumOfAllKnownRAVs() *big.Int {
	if psc.currentRAV == nil {
		return big.NewInt(0)
	}
	return psc.currentRAV.Message.ValueAggregate
}

// ShouldContinue decides whether to continue or stop (psc -> p)
func (psc *ProviderSidecar) ShouldContinue() bool {
	escrow, _ := psc.CheckEscrowFunds()
	totalRAVs := psc.CheckSumOfAllKnownRAVs()

	// Continue if escrow has enough funds to cover tracked usage
	return escrow.Cmp(totalRAVs) >= 0
}

// EndSession ends the streaming session (p -> ss, p -> psc)
func (p *Provider) EndSession() {
	zlog.Debug("Provider: ending session",
		zap.String("provider", p.name),
		zap.Uint64("total_blocks_sent", p.blocksSent))

	p.sessionActive = false
}

// CollectFinalRAV collects the final RAV on-chain via SubstreamsDataService
func (psc *ProviderSidecar) CollectFinalRAV(env *TestEnv, dataServiceAddr eth.Address, serviceProviderKey *eth.PrivateKey, dataServiceCut uint64) (uint64, error) {
	if psc.currentRAV == nil {
		return 0, nil
	}

	zlog.Info("ProviderSidecar: collecting final RAV on-chain via SubstreamsDataService",
		zap.String("sidecar", psc.name),
		zap.String("value", psc.currentRAV.Message.ValueAggregate.String()))

	// Call collect() via SubstreamsDataService
	tokensCollected, err := callDataServiceCollect(
		env.ctx,
		env.rpcClient,
		serviceProviderKey,
		env.ChainID,
		dataServiceAddr,
		serviceProviderKey.PublicKey().Address(),
		psc.currentRAV,
		dataServiceCut,
		env,
	)
	if err != nil {
		return 0, err
	}

	return tokensCollected, nil
}

// ========== Integration Test ==========

// TestSubstreamsNetworkPaymentsFlow tests the complete Substreams network payments flow
func TestSubstreamsNetworkPaymentsFlow(t *testing.T) {
	env := SetupEnv(t)
	zlog.Info("starting TestSubstreamsNetworkPaymentsFlow", zap.Uint64("chain_id", env.ChainID))

	// ============================================================================
	// SETUP: Fund escrow, set up provision, authorize signer
	// ============================================================================

	tokensToDeposit := new(big.Int)
	tokensToDeposit.SetString("10000000000000000000000", 10) // 10,000 GRT

	// Mint GRT to payer
	err := callMintGRT(env.ctx, env.rpcClient, env.DeployerKey, env.ChainID, env.GRTToken, env.PayerAddr, tokensToDeposit, env.ABIs.GRTToken)
	require.NoError(t, err, "Failed to mint GRT")

	// Approve escrow to spend GRT
	err = callApproveGRT(env.ctx, env.rpcClient, env.PayerKey, env.ChainID, env.GRTToken, env.PaymentsEscrow, tokensToDeposit, env.ABIs.GRTToken)
	require.NoError(t, err, "Failed to approve GRT")

	// Deposit to escrow
	err = callDepositEscrow(env.ctx, env.rpcClient, env.PayerKey, env.ChainID, env.PaymentsEscrow, env.CollectorAddress, env.ServiceProviderAddr, tokensToDeposit, env.ABIs.Escrow)
	require.NoError(t, err, "Failed to deposit to escrow")

	// Set up SubstreamsDataService: set provision tokens range (min = 0 for testing)
	err = callSetProvisionTokensRange(env.ctx, env.rpcClient, env.DeployerKey, env.ChainID, env.SubstreamsDataService, big.NewInt(0), env.ABIs.DataService)
	require.NoError(t, err, "Failed to set provision tokens range")

	// Set provision for service provider
	provisionTokens := new(big.Int)
	provisionTokens.SetString("1000000000000000000000", 10) // 1,000 GRT
	err = callSetProvision(env.ctx, env.rpcClient, env.DeployerKey, env.ChainID, env.Staking, env.ServiceProviderAddr, env.SubstreamsDataService, provisionTokens, 0, 0, env.ABIs.Staking)
	require.NoError(t, err, "Failed to set provision")

	// Register service provider with SubstreamsDataService
	err = callRegisterWithDataService(env.ctx, env.rpcClient, env.ServiceProviderKey, env.ChainID, env.SubstreamsDataService, env.ServiceProviderAddr, env.ServiceProviderAddr, env.ABIs.DataService)
	require.NoError(t, err, "Failed to register with data service")

	// Create signer key and authorize it
	signerKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	signerAddr := signerKey.PublicKey().Address()

	err = callAuthorizeSigner(env.ctx, env.rpcClient, env.PayerKey, env.ChainID, env.CollectorAddress, signerKey, env.ABIs.Collector)
	require.NoError(t, err, "Failed to authorize signer")

	// ============================================================================
	// CREATE FLOW PARTICIPANTS
	// ============================================================================

	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0x5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b01")[:])

	// Consumer Sidecar (sc)
	consumerSidecar := NewConsumerSidecar(
		"ConsumerSidecar",
		domain,
		signerKey,
		env.PayerAddr,
		env.ServiceProviderAddr,
		env.SubstreamsDataService,
		collectionID,
	)

	// Provider Sidecar (psc)
	providerSidecar := NewProviderSidecar(
		"ProviderSidecar",
		domain,
		[]eth.Address{signerAddr},
		collectionID,
		tokensToDeposit, // Escrow balance for checking
		env,
		env.PayerAddr,
		env.ServiceProviderKey,
	)

	// Substreams Client (ss)
	substreamsClient := NewSubstreamsClient("SubstreamsClient", consumerSidecar)

	// Provider (p)
	provider := NewProvider(
		"BlockProvider",
		providerSidecar,
		env.SubstreamsDataService,
		env.ServiceProviderKey,
		env.CollectorAddress,
		env.ServiceProviderAddr,
	)

	// ============================================================================
	// FLOW EXECUTION
	// ============================================================================

	// Step 1: ss -> sc: init()
	zlog.Info("Step 1: Substreams init()")
	err = substreamsClient.Init()
	require.NoError(t, err)

	// Step 2: sc -> psc: startSession(escrow_account, RAV0)
	zlog.Info("Step 2: startSession with initial RAV0")
	sessionResp, err := consumerSidecar.StartSession(providerSidecar, env.PaymentsEscrow)
	require.NoError(t, err)
	require.NotNil(t, sessionResp)
	assert.NotNil(t, sessionResp.AcceptedRAV, "Provider should accept initial RAV")

	// Step 3: psc -> sc: useThis(RAVx)
	// Already done in StartSession response

	// Step 4: sc -> ss: RAVx
	zlog.Info("Step 3-4: Consumer receives accepted RAV")
	consumerSidecar.ReceiveSessionResponse(sessionResp)

	// Step 5: ss -> p: Blocks() with header payment=RAVx
	zlog.Info("Step 5: Substreams requests Blocks()")
	blocksResp, err := substreamsClient.RequestBlocks(provider)
	require.NoError(t, err)
	assert.True(t, provider.sessionActive, "Provider session should be active")
	assert.Equal(t, uint64(1000), blocksResp.RequiredBlocksPreproc)

	// Step 6-7: p -> psc: validate RAVx, psc -> p: OK
	// Already done in HandleBlocksRequest

	// Step 8: p -> ss: sessionInit(required_blocks_preproc=N)
	zlog.Info("Step 8: Session initialized with preproc requirements")

	// ============================================================================
	// DATA STREAMING LOOP
	// ============================================================================

	valuePerBlock := big.NewInt(1000000000000000) // 0.001 GRT per block
	totalBlocksToStream := uint64(100)

	zlog.Info("Starting data streaming loop", zap.Uint64("total_blocks", totalBlocksToStream))

	// Simulate streaming in batches
	batchSize := uint64(25)
	for sent := uint64(0); sent < totalBlocksToStream; sent += batchSize {
		blocksToSend := batchSize
		if sent+batchSize > totalBlocksToStream {
			blocksToSend = totalBlocksToStream - sent
		}

		// Step 9: Loop - p -> ss: data..., p -> psc: sent(usage), ss -> sc: received(usage)
		usage := provider.SendData(blocksToSend, valuePerBlock)
		substreamsClient.ReceiveData(usage)

		zlog.Debug("Batch streamed",
			zap.Uint64("blocks_sent", sent+blocksToSend),
			zap.String("total_value", providerSidecar.trackedUsage.String()))
	}

	// Step 10: psc -> sc: requestRAV(RAVx, usage)
	zlog.Info("Step 10: Provider requests updated RAV")
	newRAV, err := providerSidecar.RequestRAV(consumerSidecar)
	require.NoError(t, err)
	require.NotNil(t, newRAV)

	expectedValue := new(big.Int).Mul(valuePerBlock, big.NewInt(int64(totalBlocksToStream)))
	assert.Equal(t, expectedValue.String(), newRAV.Message.ValueAggregate.String(),
		"RAV should contain total streamed value")

	// Step 11: sc -> psc: signed(RAVx)
	zlog.Info("Step 11: Consumer returns signed RAV")
	err = providerSidecar.ReceiveSignedRAV(newRAV)
	require.NoError(t, err)

	// Step 12: psc -> p: Continue() or Stop()
	zlog.Info("Step 12: Provider decides to continue or stop")
	shouldContinue := providerSidecar.ShouldContinue()
	assert.True(t, shouldContinue, "Should continue - escrow has sufficient funds")

	// ============================================================================
	// ESCROW FUNDS CHECK LOOP (optional in flow)
	// ============================================================================

	zlog.Info("Optional: Checking escrow funds")
	escrowFunds, err := providerSidecar.CheckEscrowFunds()
	require.NoError(t, err)
	assert.True(t, escrowFunds.Cmp(expectedValue) >= 0, "Escrow should have enough funds")

	ravSum := providerSidecar.CheckSumOfAllKnownRAVs()
	assert.Equal(t, expectedValue.String(), ravSum.String(), "RAV sum should match tracked usage")

	// ============================================================================
	// SESSION END
	// ============================================================================

	// Step 14: p -> ss: end()
	zlog.Info("Step 14: Provider ends session")
	provider.EndSession()
	assert.False(t, provider.sessionActive, "Session should be inactive")

	// Final usage report (ss -> sc: lastUsage - already tracked)

	// Final RAV request (psc -> sc: requestRAV)
	// Already done above - the last RAV is the final one

	// ============================================================================
	// ON-CHAIN COLLECTION
	// ============================================================================

	zlog.Info("Final: Collecting RAV on-chain via SubstreamsDataService")
	tokensCollected, err := providerSidecar.CollectFinalRAV(
		env,
		provider.dataServiceAddr,
		provider.serviceProviderKey,
		provider.dataServiceCut,
	)
	require.NoError(t, err)

	// Verify collection amount
	assert.Equal(t, expectedValue.Uint64(), tokensCollected,
		"Tokens collected should match RAV value")

	// Verify on-chain state
	collected, err := env.CallTokensCollected(env.SubstreamsDataService, collectionID, env.ServiceProviderAddr, env.PayerAddr)
	require.NoError(t, err)
	assert.Equal(t, expectedValue.Uint64(), collected,
		"On-chain tokensCollected should match expected value")

	zlog.Info("TestSubstreamsNetworkPaymentsFlow completed successfully",
		zap.Uint64("total_blocks_streamed", totalBlocksToStream),
		zap.String("total_value_collected", expectedValue.String()))

	t.Logf("Successfully completed Substreams flow: streamed %d blocks, collected %s GRT",
		totalBlocksToStream, expectedValue.String())
}

// TestSubstreamsFlowWithInsufficientEscrow tests the flow when escrow runs low
func TestSubstreamsFlowWithInsufficientEscrow(t *testing.T) {
	env := SetupEnv(t)
	zlog.Info("starting TestSubstreamsFlowWithInsufficientEscrow", zap.Uint64("chain_id", env.ChainID))

	// Setup with smaller escrow
	smallEscrow := big.NewInt(50000000000000000) // 0.05 GRT - very small

	// Mint and deposit small amount
	err := callMintGRT(env.ctx, env.rpcClient, env.DeployerKey, env.ChainID, env.GRTToken, env.PayerAddr, smallEscrow, env.ABIs.GRTToken)
	require.NoError(t, err)

	err = callApproveGRT(env.ctx, env.rpcClient, env.PayerKey, env.ChainID, env.GRTToken, env.PaymentsEscrow, smallEscrow, env.ABIs.GRTToken)
	require.NoError(t, err)

	err = callDepositEscrow(env.ctx, env.rpcClient, env.PayerKey, env.ChainID, env.PaymentsEscrow, env.CollectorAddress, env.ServiceProviderAddr, smallEscrow, env.ABIs.Escrow)
	require.NoError(t, err)

	// Set up SubstreamsDataService: set provision tokens range (min = 0 for testing)
	err = callSetProvisionTokensRange(env.ctx, env.rpcClient, env.DeployerKey, env.ChainID, env.SubstreamsDataService, big.NewInt(0), env.ABIs.DataService)
	require.NoError(t, err, "Failed to set provision tokens range")

	// Setup provision
	provisionTokens := new(big.Int)
	provisionTokens.SetString("1000000000000000000000", 10)
	err = callSetProvision(env.ctx, env.rpcClient, env.DeployerKey, env.ChainID, env.Staking, env.ServiceProviderAddr, env.SubstreamsDataService, provisionTokens, 0, 0, env.ABIs.Staking)
	require.NoError(t, err)

	// Register service provider with SubstreamsDataService
	err = callRegisterWithDataService(env.ctx, env.rpcClient, env.ServiceProviderKey, env.ChainID, env.SubstreamsDataService, env.ServiceProviderAddr, env.ServiceProviderAddr, env.ABIs.DataService)
	require.NoError(t, err, "Failed to register with data service")

	// Create and authorize signer
	signerKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	signerAddr := signerKey.PublicKey().Address()

	err = callAuthorizeSigner(env.ctx, env.rpcClient, env.PayerKey, env.ChainID, env.CollectorAddress, signerKey, env.ABIs.Collector)
	require.NoError(t, err)

	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0x5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b02")[:])

	// Create participants
	consumerSidecar := NewConsumerSidecar(
		"ConsumerSidecar",
		domain,
		signerKey,
		env.PayerAddr,
		env.ServiceProviderAddr,
		env.SubstreamsDataService,
		collectionID,
	)

	providerSidecar := NewProviderSidecar(
		"ProviderSidecar",
		domain,
		[]eth.Address{signerAddr},
		collectionID,
		smallEscrow, // Small escrow
		env,
		env.PayerAddr,
		env.ServiceProviderKey,
	)

	substreamsClient := NewSubstreamsClient("SubstreamsClient", consumerSidecar)

	provider := NewProvider(
		"BlockProvider",
		providerSidecar,
		env.SubstreamsDataService,
		env.ServiceProviderKey,
		env.CollectorAddress,
		env.ServiceProviderAddr,
	)

	// Start session
	sessionResp, err := consumerSidecar.StartSession(providerSidecar, env.PaymentsEscrow)
	require.NoError(t, err)
	consumerSidecar.ReceiveSessionResponse(sessionResp)

	_, err = substreamsClient.RequestBlocks(provider)
	require.NoError(t, err)

	// Stream data until usage exceeds escrow
	valuePerBlock := big.NewInt(10000000000000000) // 0.01 GRT per block
	blocksPerBatch := uint64(10)

	// Stream until escrow is exceeded
	for i := 0; i < 10; i++ {
		usage := provider.SendData(blocksPerBatch, valuePerBlock)
		substreamsClient.ReceiveData(usage)

		// Request RAV
		newRAV, err := providerSidecar.RequestRAV(consumerSidecar)
		require.NoError(t, err)
		err = providerSidecar.ReceiveSignedRAV(newRAV)
		require.NoError(t, err)

		// Check if we should continue
		shouldContinue := providerSidecar.ShouldContinue()
		if !shouldContinue {
			zlog.Info("Provider stopping due to insufficient escrow",
				zap.Int("batches_streamed", i+1),
				zap.String("tracked_usage", providerSidecar.trackedUsage.String()),
				zap.String("escrow_balance", smallEscrow.String()))

			// Provider should stop when escrow is insufficient
			provider.EndSession()
			break
		}
	}

	assert.False(t, provider.sessionActive, "Session should have ended due to insufficient escrow")

	// The final RAV value should be limited to what we streamed before stopping
	finalRAVValue := providerSidecar.currentRAV.Message.ValueAggregate
	assert.True(t, finalRAVValue.Cmp(smallEscrow) >= 0,
		"Final RAV value should exceed escrow (we streamed until it was exceeded)")

	zlog.Info("TestSubstreamsFlowWithInsufficientEscrow completed",
		zap.String("final_rav_value", finalRAVValue.String()),
		zap.String("escrow_balance", smallEscrow.String()))
}

// TestSubstreamsFlowMultipleRAVRequests tests multiple RAV request cycles
func TestSubstreamsFlowMultipleRAVRequests(t *testing.T) {
	env := SetupEnv(t)
	zlog.Info("starting TestSubstreamsFlowMultipleRAVRequests", zap.Uint64("chain_id", env.ChainID))

	// Setup
	tokensToDeposit := new(big.Int)
	tokensToDeposit.SetString("10000000000000000000000", 10) // 10,000 GRT

	err := callMintGRT(env.ctx, env.rpcClient, env.DeployerKey, env.ChainID, env.GRTToken, env.PayerAddr, tokensToDeposit, env.ABIs.GRTToken)
	require.NoError(t, err)

	err = callApproveGRT(env.ctx, env.rpcClient, env.PayerKey, env.ChainID, env.GRTToken, env.PaymentsEscrow, tokensToDeposit, env.ABIs.GRTToken)
	require.NoError(t, err)

	err = callDepositEscrow(env.ctx, env.rpcClient, env.PayerKey, env.ChainID, env.PaymentsEscrow, env.CollectorAddress, env.ServiceProviderAddr, tokensToDeposit, env.ABIs.Escrow)
	require.NoError(t, err)

	// Set up SubstreamsDataService: set provision tokens range (min = 0 for testing)
	err = callSetProvisionTokensRange(env.ctx, env.rpcClient, env.DeployerKey, env.ChainID, env.SubstreamsDataService, big.NewInt(0), env.ABIs.DataService)
	require.NoError(t, err, "Failed to set provision tokens range")

	provisionTokens := new(big.Int)
	provisionTokens.SetString("1000000000000000000000", 10)
	err = callSetProvision(env.ctx, env.rpcClient, env.DeployerKey, env.ChainID, env.Staking, env.ServiceProviderAddr, env.SubstreamsDataService, provisionTokens, 0, 0, env.ABIs.Staking)
	require.NoError(t, err)

	// Register service provider with SubstreamsDataService
	err = callRegisterWithDataService(env.ctx, env.rpcClient, env.ServiceProviderKey, env.ChainID, env.SubstreamsDataService, env.ServiceProviderAddr, env.ServiceProviderAddr, env.ABIs.DataService)
	require.NoError(t, err, "Failed to register with data service")

	signerKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)
	signerAddr := signerKey.PublicKey().Address()

	err = callAuthorizeSigner(env.ctx, env.rpcClient, env.PayerKey, env.ChainID, env.CollectorAddress, signerKey, env.ABIs.Collector)
	require.NoError(t, err)

	domain := horizon.NewDomain(env.ChainID, env.CollectorAddress)

	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0x5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b5b03")[:])

	// Create participants
	consumerSidecar := NewConsumerSidecar(
		"ConsumerSidecar",
		domain,
		signerKey,
		env.PayerAddr,
		env.ServiceProviderAddr,
		env.SubstreamsDataService,
		collectionID,
	)

	providerSidecar := NewProviderSidecar(
		"ProviderSidecar",
		domain,
		[]eth.Address{signerAddr},
		collectionID,
		tokensToDeposit,
		env,
		env.PayerAddr,
		env.ServiceProviderKey,
	)

	substreamsClient := NewSubstreamsClient("SubstreamsClient", consumerSidecar)

	provider := NewProvider(
		"BlockProvider",
		providerSidecar,
		env.SubstreamsDataService,
		env.ServiceProviderKey,
		env.CollectorAddress,
		env.ServiceProviderAddr,
	)

	// Initialize session
	sessionResp, err := consumerSidecar.StartSession(providerSidecar, env.PaymentsEscrow)
	require.NoError(t, err)
	consumerSidecar.ReceiveSessionResponse(sessionResp)

	_, err = substreamsClient.RequestBlocks(provider)
	require.NoError(t, err)

	// Test multiple RAV request cycles (simulating long streaming session)
	valuePerBlock := big.NewInt(1000000000000000) // 0.001 GRT per block
	numCycles := 5
	blocksPerCycle := uint64(20)

	var ravHistory []*horizon.SignedRAV
	runningTotal := big.NewInt(0)

	for cycle := 0; cycle < numCycles; cycle++ {
		// Stream data for this cycle
		usage := provider.SendData(blocksPerCycle, valuePerBlock)
		substreamsClient.ReceiveData(usage)

		// Calculate expected running total
		cycleValue := new(big.Int).Mul(valuePerBlock, big.NewInt(int64(blocksPerCycle)))
		runningTotal.Add(runningTotal, cycleValue)

		// Request RAV
		newRAV, err := providerSidecar.RequestRAV(consumerSidecar)
		require.NoError(t, err)
		require.NotNil(t, newRAV)

		// Verify RAV value is cumulative
		assert.Equal(t, runningTotal.String(), newRAV.Message.ValueAggregate.String(),
			"RAV %d should have cumulative value", cycle+1)

		// Verify timestamp is increasing
		if len(ravHistory) > 0 {
			prevRAV := ravHistory[len(ravHistory)-1]
			assert.True(t, newRAV.Message.TimestampNs > prevRAV.Message.TimestampNs,
				"RAV timestamp should increase")
		}

		err = providerSidecar.ReceiveSignedRAV(newRAV)
		require.NoError(t, err)

		ravHistory = append(ravHistory, newRAV)

		zlog.Debug("Completed RAV cycle",
			zap.Int("cycle", cycle+1),
			zap.String("rav_value", newRAV.Message.ValueAggregate.String()))
	}

	// End session
	provider.EndSession()

	// Collect final RAV on-chain via SubstreamsDataService
	tokensCollected, err := providerSidecar.CollectFinalRAV(
		env,
		provider.dataServiceAddr,
		provider.serviceProviderKey,
		provider.dataServiceCut,
	)
	require.NoError(t, err)

	// Verify final collection
	expectedTotal := new(big.Int).Mul(valuePerBlock, big.NewInt(int64(numCycles*int(blocksPerCycle))))
	assert.Equal(t, expectedTotal.Uint64(), tokensCollected,
		"Final collection should match total streamed value")

	zlog.Info("TestSubstreamsFlowMultipleRAVRequests completed",
		zap.Int("num_rav_cycles", numCycles),
		zap.String("total_collected", expectedTotal.String()))
}
