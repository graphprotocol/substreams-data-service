package integration

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/streamingfast/eth-go"
	"github.com/streamingfast/eth-go/rpc"
	"github.com/streamingfast/eth-go/signer/native"
	horizon "github.com/streamingfast/horizon-go"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"
)

// mustNewCollectionID creates a CollectionID from a hex string or panics
func mustNewCollectionID(hexStr string) horizon.CollectionID {
	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash(hexStr)[:])
	return collectionID
}

// Account represents an Ethereum account with its private key and address
type Account struct {
	Address    eth.Address
	PrivateKey *eth.PrivateKey
}

// mustNewAccount creates a new random account or panics on failure
func mustNewAccount() Account {
	key, err := eth.NewRandomPrivateKey()
	if err != nil {
		panic(fmt.Sprintf("generating random private key: %v", err))
	}
	return Account{
		Address:    key.PublicKey().Address(),
		PrivateKey: key,
	}
}

// Contract represents a deployed contract with its address and ABI
type Contract struct {
	Address eth.Address
	ABI     *eth.ABI
}

// CallData encodes a contract method call with arguments and returns the calldata
func (c *Contract) CallData(method string, args ...interface{}) ([]byte, error) {
	fn := c.ABI.FindFunctionByName(method)
	if fn == nil {
		return nil, fmt.Errorf("%s function not found in ABI", method)
	}

	data, err := fn.NewCall(args...).Encode()
	if err != nil {
		return nil, fmt.Errorf("encoding %s call: %w", method, err)
	}

	return data, nil
}

// MustCallData encodes a contract method call and panics on error
func (c *Contract) MustCallData(method string, args ...interface{}) []byte {
	data, err := c.CallData(method, args...)
	if err != nil {
		panic(err)
	}
	return data
}

// mustLoadContract loads a contract ABI from artifact and returns a Contract with zero address
func mustLoadContract(name string) *Contract {
	abi, err := loadABI(name)
	if err != nil {
		panic(fmt.Sprintf("loading %s ABI: %v", name, err))
	}
	return &Contract{ABI: abi}
}

// ContractArtifact represents a compiled Foundry contract
type ContractArtifact struct {
	ABI      json.RawMessage `json:"abi"`
	Bytecode struct {
		Object string `json:"object"`
	} `json:"bytecode"`
}

// TestEnv holds the test environment state
type TestEnv struct {
	ctx            context.Context
	cancel         context.CancelFunc
	anvilContainer testcontainers.Container
	rpcClient      *rpc.Client
	ChainID        uint64

	// Contracts (ABI loaded at init, address set after deployment)
	GRTToken      *Contract
	Controller    *Contract
	Staking       *Contract
	Escrow        *Contract
	GraphPayments *Contract
	Collector     *Contract
	DataService   *Contract

	// Test accounts
	Deployer        Account
	ServiceProvider Account
	Payer           Account
}

var (
	sharedEnv     *TestEnv
	sharedEnvOnce sync.Once
	sharedEnvErr  error
)

// SetupEnv returns a shared test environment
func SetupEnv(t *testing.T) *TestEnv {
	t.Helper()
	sharedEnvOnce.Do(func() {
		sharedEnv, sharedEnvErr = setupEnv()
	})
	require.NoError(t, sharedEnvErr, "Failed to setup test environment")
	t.Cleanup(func() {
		// Don't cleanup here since we're using shared env
	})
	return sharedEnv
}

func setupEnv() (*TestEnv, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

	zlog.Info("starting test environment setup")

	// Pre-load all contracts (ABIs loaded now, addresses set after deployment)
	zlog.Debug("pre-loading contract ABIs")
	grtToken := mustLoadContract("MockGRTToken")
	controller := mustLoadContract("MockController")
	staking := mustLoadContract("MockStaking")
	escrow := mustLoadContract("PaymentsEscrow")
	graphPayments := mustLoadContract("GraphPayments")
	collector := mustLoadContract("GraphTallyCollector")
	dataService := mustLoadContract("SubstreamsDataService")

	// Start Anvil container (Foundry's local Ethereum node)
	// Anvil provides deterministic chain ID behavior, unlike Geth dev mode
	// Note: The foundry container uses /bin/sh -c as entrypoint, so we pass the command as a single string
	zlog.Debug("creating Anvil container request")
	anvilReq := testcontainers.ContainerRequest{
		Image: "ghcr.io/foundry-rs/foundry:latest",
		Cmd: []string{
			"anvil --host 0.0.0.0 --port 8545 --chain-id 1337 --block-time 1",
		},
		ExposedPorts: []string{"8545/tcp"},
		WaitingFor: wait.ForListeningPort("8545/tcp").
			WithStartupTimeout(60 * time.Second),
	}

	zlog.Debug("starting Anvil container")
	anvilContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: anvilReq,
		Started:          true,
	})
	if err != nil {
		zlog.Error("failed to start Anvil container", zap.Error(err))
		cancel()
		return nil, fmt.Errorf("starting anvil container: %w", err)
	}
	zlog.Info("Anvil container started successfully")

	mappedPort, err := anvilContainer.MappedPort(ctx, "8545/tcp")
	if err != nil {
		zlog.Error("failed to get mapped port", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("getting mapped port: %w", err)
	}

	host, err := anvilContainer.Host(ctx)
	if err != nil {
		zlog.Error("failed to get container host", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("getting host: %w", err)
	}

	rpcURL := fmt.Sprintf("http://%s:%s", host, mappedPort.Port())
	zlog.Info("Anvil RPC endpoint ready", zap.String("rpc_url", rpcURL))

	// Create RPC client
	rpcClient := rpc.NewClient(rpcURL)

	// Wait for RPC to be responsive and get the chain ID
	zlog.Info("querying chain ID from Anvil node")
	var chainIDInt *big.Int
	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		zlog.Debug("attempting to query chain ID", zap.Int("attempt", i+1))
		chainIDInt, err = rpcClient.ChainID(ctx)
		if err == nil && chainIDInt != nil && chainIDInt.Sign() > 0 {
			zlog.Info("chain ID successfully retrieved", zap.Uint64("chain_id", chainIDInt.Uint64()))
			break
		} else {
			zlog.Debug("chain ID query failed", zap.Error(err))
		}
	}
	if chainIDInt == nil {
		zlog.Error("failed to get valid chain ID after all retries")
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("failed to get valid chain ID after retries")
	}

	// Get dev account (funded by Anvil)
	zlog.Debug("querying dev accounts")
	accounts, err := rpc.Do[[]string](rpcClient, ctx, "eth_accounts", nil)
	if err != nil || len(accounts) == 0 {
		zlog.Error("failed to get dev accounts", zap.Error(err), zap.Int("num_accounts", len(accounts)))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("getting dev accounts: %w", err)
	}
	devAccount := eth.MustNewAddress(accounts[0])
	zlog.Info("dev account retrieved", zap.Stringer("dev_account", devAccount))

	// Create test accounts
	zlog.Debug("generating test accounts")
	deployer := mustNewAccount()
	serviceProvider := mustNewAccount()
	payer := mustNewAccount()
	zlog.Debug("generated test accounts",
		zap.Stringer("deployer", deployer.Address),
		zap.Stringer("service_provider", serviceProvider.Address),
		zap.Stringer("payer", payer.Address),
	)

	// Fund deployer from dev account (10 ETH)
	zlog.Debug("funding deployer account")
	fundAmount := new(big.Int)
	fundAmount.SetString("10000000000000000000", 10) // 10 ETH
	if err := fundFromDevAccount(ctx, rpcClient, devAccount, deployer.Address, fundAmount); err != nil {
		zlog.Error("failed to fund deployer", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("funding deployer: %w", err)
	}
	zlog.Debug("deployer funded successfully", zap.String("amount", "10 ETH"))

	// Fund payer account (5 ETH for gas)
	zlog.Debug("funding payer account")
	fundAmount2 := new(big.Int)
	fundAmount2.SetString("5000000000000000000", 10) // 5 ETH
	if err := fundFromDevAccount(ctx, rpcClient, devAccount, payer.Address, fundAmount2); err != nil {
		zlog.Error("failed to fund payer", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("funding payer: %w", err)
	}
	zlog.Debug("payer funded successfully", zap.String("amount", "5 ETH"))

	// Fund service provider account (2 ETH for gas to call SubstreamsDataService)
	zlog.Debug("funding service provider account")
	fundAmount3 := new(big.Int)
	fundAmount3.SetString("2000000000000000000", 10) // 2 ETH
	if err := fundFromDevAccount(ctx, rpcClient, devAccount, serviceProvider.Address, fundAmount3); err != nil {
		zlog.Error("failed to fund service provider", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("funding service provider: %w", err)
	}
	zlog.Debug("service provider funded successfully", zap.String("amount", "2 ETH"))

	chainID := chainIDInt.Uint64()

	// ============================================================================
	// PHASE 1: Deploy all MOCK infrastructure contracts
	// These are minimal implementations that satisfy GraphDirectory dependencies
	// ============================================================================
	zlog.Info("Phase 1: Deploying mock infrastructure contracts")

	// 1. Deploy MockGRTToken
	zlog.Debug("loading MockGRTToken artifact")
	grtArtifact, err := loadContractArtifact("MockGRTToken")
	if err != nil {
		zlog.Error("failed to load GRT artifact", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading GRT artifact: %w", err)
	}
	grtToken.Address, err = deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, grtArtifact, nil) // no constructor args
	if err != nil {
		zlog.Error("failed to deploy GRT token", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying GRT: %w", err)
	}
	zlog.Info("MockGRTToken deployed", zap.Stringer("address", grtToken.Address))

	// 2. Deploy MockController
	controllerArtifact, err := loadContractArtifact("MockController")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading Controller artifact: %w", err)
	}
	controller.Address, err = deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, controllerArtifact, controller.ABI, deployer.Address)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying Controller: %w", err)
	}
	zlog.Info("MockController deployed", zap.Stringer("address", controller.Address))

	// 3. Deploy MockStaking
	stakingArtifact, err := loadContractArtifact("MockStaking")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading Staking artifact: %w", err)
	}
	staking.Address, err = deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, stakingArtifact, nil)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying Staking: %w", err)
	}
	zlog.Info("MockStaking deployed", zap.Stringer("address", staking.Address))

	// Set GRT token in MockStaking (needed for addToDelegationPool and stakeTo)
	if err := callSetGraphToken(ctx, rpcClient, deployer.PrivateKey, chainID, staking.Address, grtToken.Address, staking.ABI); err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("setting GRT token in staking: %w", err)
	}
	zlog.Debug("set GRT token in MockStaking")

	// 4. Deploy MockEpochManager
	epochManagerArtifact, err := loadContractArtifact("MockEpochManager")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading EpochManager artifact: %w", err)
	}
	epochManagerAddr, err := deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, epochManagerArtifact, nil)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying EpochManager: %w", err)
	}
	zlog.Info("MockEpochManager deployed", zap.Stringer("address", epochManagerAddr))

	// 5. Deploy MockRewardsManager
	rewardsManagerArtifact, err := loadContractArtifact("MockRewardsManager")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading RewardsManager artifact: %w", err)
	}
	rewardsManagerAddr, err := deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, rewardsManagerArtifact, nil)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying RewardsManager: %w", err)
	}
	zlog.Info("MockRewardsManager deployed", zap.Stringer("address", rewardsManagerAddr))

	// 6. Deploy MockTokenGateway
	tokenGatewayArtifact, err := loadContractArtifact("MockTokenGateway")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading TokenGateway artifact: %w", err)
	}
	tokenGatewayAddr, err := deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, tokenGatewayArtifact, nil)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying TokenGateway: %w", err)
	}
	zlog.Info("MockTokenGateway deployed", zap.Stringer("address", tokenGatewayAddr))

	// 7. Deploy MockProxyAdmin
	proxyAdminArtifact, err := loadContractArtifact("MockProxyAdmin")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading ProxyAdmin artifact: %w", err)
	}
	proxyAdminAddr, err := deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, proxyAdminArtifact, nil)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying ProxyAdmin: %w", err)
	}
	zlog.Info("MockProxyAdmin deployed", zap.Stringer("address", proxyAdminAddr))

	// 8. Deploy MockCuration
	curationArtifact, err := loadContractArtifact("MockCuration")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading Curation artifact: %w", err)
	}
	curationAddr, err := deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, curationArtifact, nil)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying Curation: %w", err)
	}
	zlog.Info("MockCuration deployed", zap.Stringer("address", curationAddr))

	// ============================================================================
	// PHASE 2: Register ALL contracts in Controller with PLACEHOLDER addresses
	// This allows original contracts to be deployed (they read from Controller)
	// ============================================================================
	zlog.Info("Phase 2: Registering contracts in Controller")

	// We need placeholder addresses for GraphPayments and PaymentsEscrow
	// Use deployer address as placeholder - will be overwritten
	placeholderAddr := deployer.Address

	// Register all GraphDirectory dependencies
	registrations := []struct {
		name string
		addr eth.Address
	}{
		{"GraphToken", grtToken.Address},
		{"Staking", staking.Address},
		{"HorizonStaking", staking.Address},
		{"EpochManager", epochManagerAddr},
		{"RewardsManager", rewardsManagerAddr},
		{"GraphTokenGateway", tokenGatewayAddr},
		{"GraphProxyAdmin", proxyAdminAddr},
		{"Curation", curationAddr},
		{"GraphPayments", placeholderAddr},  // Placeholder - will be overwritten
		{"PaymentsEscrow", placeholderAddr}, // Placeholder - will be overwritten
	}

	for _, reg := range registrations {
		if err := callSetContractProxy(ctx, rpcClient, deployer.PrivateKey, chainID, controller.Address, reg.name, reg.addr, controller.ABI); err != nil {
			anvilContainer.Terminate(ctx)
			cancel()
			return nil, fmt.Errorf("registering %s in controller: %w", reg.name, err)
		}
		zlog.Debug("registered contract in Controller", zap.String("name", reg.name), zap.Stringer("address", reg.addr))
	}

	// ============================================================================
	// PHASE 3: Deploy ORIGINAL GraphPayments contract
	// ============================================================================
	zlog.Info("Phase 3: Deploying original GraphPayments")

	graphPaymentsArtifact, err := loadContractArtifact("GraphPayments")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading GraphPayments artifact: %w", err)
	}
	// GraphPayments constructor: (address controller, uint256 protocolPaymentCut)
	// Protocol cut in PPM (parts per million): 10000 = 1%
	protocolCut := big.NewInt(10000) // 1% protocol cut
	graphPayments.Address, err = deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, graphPaymentsArtifact, graphPayments.ABI, controller.Address, protocolCut)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying GraphPayments: %w", err)
	}
	zlog.Info("ORIGINAL GraphPayments deployed", zap.Stringer("address", graphPayments.Address))

	// NOTE: We don't call initialize() because:
	// 1. The constructor calls _disableInitializers() (designed for proxy patterns)
	// 2. We don't need Multicall functionality for our tests
	// 3. The contract works correctly without initialization

	// Update Controller with real GraphPayments address
	if err := callSetContractProxy(ctx, rpcClient, deployer.PrivateKey, chainID, controller.Address, "GraphPayments", graphPayments.Address, controller.ABI); err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("updating GraphPayments in controller: %w", err)
	}
	zlog.Debug("updated GraphPayments address in Controller")

	// ============================================================================
	// PHASE 4: Deploy ORIGINAL PaymentsEscrow contract
	// ============================================================================
	zlog.Info("Phase 4: Deploying original PaymentsEscrow")

	escrowArtifact, err := loadContractArtifact("PaymentsEscrow")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading PaymentsEscrow artifact: %w", err)
	}
	// PaymentsEscrow constructor: (address controller, uint256 withdrawEscrowThawingPeriod)
	// Thawing period in seconds: 0 for testing (no wait)
	thawingPeriod := big.NewInt(0)
	escrow.Address, err = deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, escrowArtifact, escrow.ABI, controller.Address, thawingPeriod)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying PaymentsEscrow: %w", err)
	}
	zlog.Info("ORIGINAL PaymentsEscrow deployed", zap.Stringer("address", escrow.Address))

	// NOTE: We don't call initialize() because:
	// 1. The constructor calls _disableInitializers() (designed for proxy patterns)
	// 2. We don't need Multicall functionality for our tests
	// 3. The contract works correctly without initialization

	// Update Controller with real PaymentsEscrow address
	if err := callSetContractProxy(ctx, rpcClient, deployer.PrivateKey, chainID, controller.Address, "PaymentsEscrow", escrow.Address, controller.ABI); err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("updating PaymentsEscrow in controller: %w", err)
	}
	zlog.Debug("updated PaymentsEscrow address in Controller")

	// ============================================================================
	// PHASE 5: Deploy ORIGINAL GraphTallyCollector
	// ============================================================================
	zlog.Info("Phase 5: Deploying original GraphTallyCollector")

	collectorArtifact, err := loadContractArtifact("GraphTallyCollector")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading Collector artifact: %w", err)
	}
	// Constructor: (string eip712Name, string eip712Version, address controller, uint256 revokeSignerThawingPeriod)
	collector.Address, err = deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, collectorArtifact, collector.ABI, "GraphTallyCollector", "1", controller.Address, big.NewInt(0))
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying Collector: %w", err)
	}
	zlog.Info("ORIGINAL GraphTallyCollector deployed", zap.Stringer("address", collector.Address))

	// ============================================================================
	// PHASE 6: Deploy SubstreamsDataService contract
	// ============================================================================
	zlog.Info("Phase 6: Deploying SubstreamsDataService")

	dataServiceArtifact, err := loadContractArtifact("SubstreamsDataService")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading SubstreamsDataService artifact: %w", err)
	}
	// SubstreamsDataService constructor: (address controller, address graphTallyCollector)
	dataService.Address, err = deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, dataServiceArtifact, dataService.ABI, controller.Address, collector.Address)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying SubstreamsDataService: %w", err)
	}
	zlog.Info("SubstreamsDataService deployed", zap.Stringer("address", dataService.Address))

	fmt.Printf("\n")
	fmt.Printf("============================================================\n")
	fmt.Printf("Test environment ready - using ORIGINAL Graph Protocol contracts\n")
	fmt.Printf("============================================================\n")
	fmt.Printf("  RPC URL: %s\n", rpcURL)
	fmt.Printf("  Chain ID: %d\n", chainID)
	fmt.Printf("  Deployer: %s\n", deployer.Address.Pretty())
	fmt.Printf("\n")
	fmt.Printf("ORIGINAL CONTRACTS (from horizon-contracts):\n")
	fmt.Printf("  GraphPayments: %s\n", graphPayments.Address.Pretty())
	fmt.Printf("  PaymentsEscrow: %s\n", escrow.Address.Pretty())
	fmt.Printf("  GraphTallyCollector: %s\n", collector.Address.Pretty())
	fmt.Printf("  SubstreamsDataService: %s\n", dataService.Address.Pretty())
	fmt.Printf("\n")
	fmt.Printf("MOCK CONTRACTS (test infrastructure):\n")
	fmt.Printf("  MockGRTToken: %s\n", grtToken.Address.Pretty())
	fmt.Printf("  MockController: %s\n", controller.Address.Pretty())
	fmt.Printf("  MockStaking: %s\n", staking.Address.Pretty())
	fmt.Printf("\n")
	fmt.Printf("TEST ACCOUNTS:\n")
	fmt.Printf("  Service Provider: %s\n", serviceProvider.Address.Pretty())
	fmt.Printf("  Payer: %s\n", payer.Address.Pretty())
	fmt.Printf("============================================================\n")

	return &TestEnv{
		ctx:             ctx,
		cancel:          cancel,
		anvilContainer:  anvilContainer,
		rpcClient:       rpcClient,
		ChainID:         chainID,
		GRTToken:        grtToken,
		Controller:      controller,
		Staking:         staking,
		Escrow:          escrow,
		GraphPayments:   graphPayments,
		Collector:       collector,
		DataService:     dataService,
		Deployer:        deployer,
		ServiceProvider: serviceProvider,
		Payer:           payer,
	}, nil
}

func loadContractArtifact(name string) (*ContractArtifact, error) {
	testDir := getTestDir()
	artifactPath := filepath.Join(testDir, "testdata", "contracts", name+".json")

	data, err := os.ReadFile(artifactPath)
	if err != nil {
		return nil, fmt.Errorf("reading artifact file: %w", err)
	}

	var artifact ContractArtifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		return nil, fmt.Errorf("parsing artifact: %w", err)
	}

	return &artifact, nil
}

// loadABI loads an ABI from a Foundry contract artifact JSON file
func loadABI(name string) (*eth.ABI, error) {
	testDir := getTestDir()
	artifactPath := filepath.Join(testDir, "testdata", "contracts", name+".json")
	return eth.ParseABI(artifactPath)
}

func fundFromDevAccount(ctx context.Context, rpcClient *rpc.Client, from, to eth.Address, amount *big.Int) error {
	params := []interface{}{
		map[string]interface{}{
			"from":  from.Pretty(),
			"to":    to.Pretty(),
			"value": fmt.Sprintf("0x%x", amount),
		},
	}

	// eth_sendTransaction with unsigned tx is Anvil-specific (dev account only)
	txHash, err := rpc.Do[string](rpcClient, ctx, "eth_sendTransaction", params)
	if err != nil {
		return fmt.Errorf("sending fund transaction: %w", err)
	}

	return waitForReceipt(ctx, rpcClient, txHash)
}

func deployContract(ctx context.Context, rpcClient *rpc.Client, key *eth.PrivateKey, chainID uint64, artifact *ContractArtifact, abi *eth.ABI, constructorArgs ...interface{}) (eth.Address, error) {
	bytecode := artifact.Bytecode.Object
	if strings.HasPrefix(bytecode, "0x") {
		bytecode = bytecode[2:]
	}

	deployerAddr := key.PublicKey().Address()
	zlog.Debug("deploying contract from address", zap.Stringer("deployer", deployerAddr), zap.Uint64("chain_id", chainID))

	// Get nonce
	nonce, err := rpcClient.Nonce(ctx, deployerAddr, nil)
	if err != nil {
		zlog.Error("failed to get nonce for contract deployment", zap.Error(err), zap.Stringer("deployer", deployerAddr))
		return eth.Address{}, fmt.Errorf("getting nonce: %w", err)
	}
	zlog.Debug("got nonce for deployment", zap.Uint64("nonce", nonce))

	// Get gas price
	gasPrice, err := rpcClient.GasPrice(ctx)
	if err != nil {
		return eth.Address{}, fmt.Errorf("getting gas price: %w", err)
	}

	// Gas limit estimate for contract deployment
	gasLimit := uint64(5000000) // Increased for larger original contracts

	bytecodeBytes, err := hex.DecodeString(bytecode)
	if err != nil {
		return eth.Address{}, fmt.Errorf("decoding bytecode: %w", err)
	}

	// Encode and append constructor args if provided
	data := bytecodeBytes
	if len(constructorArgs) > 0 {
		constructor := abi.FindConstructor()
		if constructor == nil {
			return eth.Address{}, fmt.Errorf("contract has no constructor but args were provided")
		}
		encodedArgs, err := constructor.NewCall(constructorArgs...).Encode()
		if err != nil {
			return eth.Address{}, fmt.Errorf("encoding constructor args: %w", err)
		}
		data = append(data, encodedArgs...)
	}

	// Create signer and sign transaction using eth-go
	signer, err := native.NewPrivateKeySigner(zlog, big.NewInt(int64(chainID)), key)
	if err != nil {
		return eth.Address{}, fmt.Errorf("creating signer: %w", err)
	}

	zlog.Debug("signing deployment transaction", zap.Uint64("chain_id", chainID))
	signedTx, err := signer.SignTransaction(nonce, nil, big.NewInt(0), gasLimit, gasPrice, data)
	if err != nil {
		zlog.Error("failed to sign deployment transaction", zap.Error(err), zap.Uint64("chain_id", chainID))
		return eth.Address{}, fmt.Errorf("signing transaction: %w", err)
	}

	// Send raw transaction
	zlog.Debug("sending deployment transaction")
	txHash, err := rpcClient.SendRawTransaction(ctx, signedTx)
	if err != nil {
		zlog.Error("failed to send deployment transaction", zap.Error(err))
		return eth.Address{}, fmt.Errorf("sending transaction: %w", err)
	}
	zlog.Debug("deployment transaction sent", zap.String("tx_hash", txHash))

	// Wait for receipt
	if err := waitForReceipt(ctx, rpcClient, txHash); err != nil {
		zlog.Error("failed to get receipt for deployment transaction", zap.Error(err), zap.String("tx_hash", txHash))
		return eth.Address{}, fmt.Errorf("waiting for receipt: %w", err)
	}

	// Get receipt to find contract address
	receipt, err := rpcClient.TransactionReceipt(ctx, eth.MustNewHash(txHash))
	if err != nil {
		zlog.Error("failed to get receipt", zap.Error(err), zap.String("tx_hash", txHash))
		return eth.Address{}, fmt.Errorf("getting receipt: %w", err)
	}
	if receipt == nil {
		zlog.Error("receipt is nil", zap.String("tx_hash", txHash))
		return eth.Address{}, fmt.Errorf("receipt is nil")
	}

	if receipt.ContractAddress == nil {
		zlog.Error("contract address not found in receipt", zap.String("tx_hash", txHash))
		return eth.Address{}, fmt.Errorf("contract address not in receipt")
	}

	contractAddr := *receipt.ContractAddress
	zlog.Debug("contract deployed successfully", zap.Stringer("contract_address", contractAddr), zap.String("tx_hash", txHash))
	return contractAddr, nil
}

func waitForReceipt(ctx context.Context, rpcClient *rpc.Client, txHash string) error {
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	hash := eth.MustNewHash(txHash)
	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for transaction %s", txHash)
		case <-ticker.C:
			receipt, err := rpcClient.TransactionReceipt(ctx, hash)
			if err != nil || receipt == nil {
				continue // Not mined yet
			}
			if receipt.Status != nil && uint64(*receipt.Status) == 0 {
				return fmt.Errorf("transaction failed: %s", txHash)
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// CallContract makes a contract call
func (env *TestEnv) CallContract(to eth.Address, data []byte) ([]byte, error) {
	params := rpc.CallParams{
		To:   to,
		Data: data,
	}

	resultHex, err := env.rpcClient.Call(env.ctx, params)
	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(resultHex, "0x") {
		resultHex = resultHex[2:]
	}

	return hex.DecodeString(resultHex)
}

// Cleanup terminates the test environment
func (env *TestEnv) Cleanup() {
	if env.anvilContainer != nil {
		env.anvilContainer.Terminate(env.ctx)
	}
	env.cancel()
}

// TestSetupConfig holds configuration for test setup
type TestSetupConfig struct {
	EscrowAmount    *big.Int // Amount to deposit in escrow (default: 10,000 GRT)
	ProvisionAmount *big.Int // Amount for service provider provision (default: 1,000 GRT)
}

// DefaultTestSetupConfig returns the default test setup configuration
func DefaultTestSetupConfig() *TestSetupConfig {
	escrow := new(big.Int)
	escrow.SetString("10000000000000000000000", 10) // 10,000 GRT

	provision := new(big.Int)
	provision.SetString("1000000000000000000000", 10) // 1,000 GRT

	return &TestSetupConfig{
		EscrowAmount:    escrow,
		ProvisionAmount: provision,
	}
}

// TestSetupResult holds the result of a test setup including the authorized signer
type TestSetupResult struct {
	SignerKey  *eth.PrivateKey
	SignerAddr eth.Address
}

// SetupTestWithSigner performs common test setup: fund escrow, set provision, register, and authorize signer
func SetupTestWithSigner(t *testing.T, env *TestEnv, config *TestSetupConfig) *TestSetupResult {
	t.Helper()

	if config == nil {
		config = DefaultTestSetupConfig()
	}

	// Mint GRT to payer
	require.NoError(t, callMintGRT(env, env.Payer.Address, config.EscrowAmount), "Failed to mint GRT")

	// Approve escrow to spend GRT
	require.NoError(t, callApproveGRT(env, config.EscrowAmount), "Failed to approve GRT")

	// Deposit to escrow
	require.NoError(t, callDepositEscrow(env, config.EscrowAmount), "Failed to deposit to escrow")

	// Set provision tokens range (min = 0 for testing)
	require.NoError(t, callSetProvisionTokensRange(env, big.NewInt(0)), "Failed to set provision tokens range")

	// Set provision for service provider
	require.NoError(t, callSetProvision(env, config.ProvisionAmount, 0, 0), "Failed to set provision")

	// Register service provider with SubstreamsDataService
	require.NoError(t, callRegisterWithDataService(env), "Failed to register with data service")

	// Create and authorize signer
	signerKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err, "Failed to create signer key")

	require.NoError(t, callAuthorizeSigner(env, signerKey), "Failed to authorize signer")

	return &TestSetupResult{
		SignerKey:  signerKey,
		SignerAddr: signerKey.PublicKey().Address(),
	}
}

// callSetContractProxy calls Controller.setContractProxy(bytes32 id, address contractAddress)
func callSetContractProxy(ctx context.Context, rpcClient *rpc.Client, key *eth.PrivateKey, chainID uint64, controllerAddr eth.Address, name string, contractAddr eth.Address, controllerABI *eth.ABI) error {
	setContractProxyFn := controllerABI.FindFunctionByName("setContractProxy")
	if setContractProxyFn == nil {
		return fmt.Errorf("setContractProxy function not found in ABI")
	}

	// The id parameter is keccak256(name) - eth-go expects []byte for bytes32
	nameHash := eth.Keccak256([]byte(name))

	data, err := setContractProxyFn.NewCall(nameHash, contractAddr).Encode()
	if err != nil {
		return fmt.Errorf("encoding setContractProxy call: %w", err)
	}

	return sendTransaction(ctx, rpcClient, key, chainID, &controllerAddr, big.NewInt(0), data)
}

// callSetGraphToken calls MockStaking.setGraphToken(address token)
func callSetGraphToken(ctx context.Context, rpcClient *rpc.Client, key *eth.PrivateKey, chainID uint64, stakingAddr eth.Address, tokenAddr eth.Address, stakingABI *eth.ABI) error {
	setGraphTokenFn := stakingABI.FindFunctionByName("setGraphToken")
	if setGraphTokenFn == nil {
		return fmt.Errorf("setGraphToken function not found in ABI")
	}

	data, err := setGraphTokenFn.NewCall(tokenAddr).Encode()
	if err != nil {
		return fmt.Errorf("encoding setGraphToken call: %w", err)
	}

	return sendTransaction(ctx, rpcClient, key, chainID, &stakingAddr, big.NewInt(0), data)
}

// sendTransaction sends a transaction and waits for receipt
func sendTransaction(ctx context.Context, rpcClient *rpc.Client, key *eth.PrivateKey, chainID uint64, to *eth.Address, value *big.Int, data []byte) error {
	from := key.PublicKey().Address()

	toStr := "contract_creation"
	var toBytes []byte
	if to != nil {
		toStr = to.Pretty()
		toBytes = (*to)[:]
	}
	zlog.Debug("sending transaction", zap.Stringer("from", from), zap.String("to", toStr), zap.Uint64("chain_id", chainID))

	// Get nonce
	nonce, err := rpcClient.Nonce(ctx, from, nil)
	if err != nil {
		zlog.Error("failed to get nonce", zap.Error(err), zap.Stringer("from", from))
		return fmt.Errorf("getting nonce: %w", err)
	}
	zlog.Debug("got nonce", zap.Uint64("nonce", nonce))

	// Get gas price
	gasPrice, err := rpcClient.GasPrice(ctx)
	if err != nil {
		return fmt.Errorf("getting gas price: %w", err)
	}

	gasLimit := uint64(500000)

	// Create signer and sign transaction using eth-go
	signer, err := native.NewPrivateKeySigner(zlog, big.NewInt(int64(chainID)), key)
	if err != nil {
		return fmt.Errorf("creating signer: %w", err)
	}

	zlog.Debug("signing transaction", zap.Uint64("chain_id", chainID))
	signedTx, err := signer.SignTransaction(nonce, toBytes, value, gasLimit, gasPrice, data)
	if err != nil {
		zlog.Error("failed to sign transaction", zap.Error(err), zap.Uint64("chain_id", chainID))
		return fmt.Errorf("signing transaction: %w", err)
	}

	// Send
	zlog.Debug("submitting transaction to RPC")
	txHash, err := rpcClient.SendRawTransaction(ctx, signedTx)
	if err != nil {
		zlog.Error("failed to send transaction", zap.Error(err))
		return fmt.Errorf("sending transaction: %w", err)
	}
	zlog.Debug("transaction submitted", zap.String("tx_hash", txHash))

	err = waitForReceipt(ctx, rpcClient, txHash)
	if err != nil {
		zlog.Error("transaction failed", zap.Error(err), zap.String("tx_hash", txHash))
	} else {
		zlog.Debug("transaction confirmed", zap.String("tx_hash", txHash))
	}
	return err
}
