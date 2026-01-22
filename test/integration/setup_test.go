package integration

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/streamingfast/eth-go"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"
)

// ContractArtifact represents a compiled Foundry contract
type ContractArtifact struct {
	ABI      json.RawMessage `json:"abi"`
	Bytecode struct {
		Object string `json:"object"`
	} `json:"bytecode"`
}

// ABIs holds all loaded contract ABIs
type ABIs struct {
	GRTToken    *eth.ABI
	Staking     *eth.ABI
	Escrow      *eth.ABI
	Collector   *eth.ABI
	DataService *eth.ABI
	Controller  *eth.ABI
}

// TestEnv holds the test environment state
type TestEnv struct {
	ctx                   context.Context
	cancel                context.CancelFunc
	anvilContainer        testcontainers.Container
	rpcURL                string
	ChainID               uint64
	GRTToken              eth.Address
	Controller            eth.Address
	Staking               eth.Address
	PaymentsEscrow        eth.Address
	GraphPayments         eth.Address
	CollectorAddress      eth.Address
	SubstreamsDataService eth.Address
	DeployerKey           *eth.PrivateKey
	DeployerAddress       eth.Address
	ServiceProviderKey    *eth.PrivateKey
	ServiceProviderAddr   eth.Address
	PayerKey              *eth.PrivateKey
	PayerAddr             eth.Address
	// ABIs for contract interactions
	ABIs *ABIs
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

	// Wait for RPC to be responsive and get the chain ID
	zlog.Info("querying chain ID from Anvil node")
	var chainIDInt *big.Int
	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		zlog.Debug("attempting to query chain ID", zap.Int("attempt", i+1))
		chainIDHex, err := rpcCall[string](ctx, rpcURL, "eth_chainId", nil)
		if err == nil && chainIDHex != "" {
			zlog.Debug("received chain ID response", zap.String("chain_id_hex", chainIDHex))
			chainIDInt, _ = new(big.Int).SetString(chainIDHex[2:], 16)
			if chainIDInt != nil && chainIDInt.Sign() > 0 {
				zlog.Info("chain ID successfully retrieved", zap.Uint64("chain_id", chainIDInt.Uint64()), zap.String("chain_id_hex", chainIDHex))
				break
			}
		} else {
			zlog.Debug("chain ID query failed", zap.Error(err), zap.String("chain_id_hex", chainIDHex))
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
	accounts, err := rpcCall[[]string](ctx, rpcURL, "eth_accounts", nil)
	if err != nil || len(accounts) == 0 {
		zlog.Error("failed to get dev accounts", zap.Error(err), zap.Int("num_accounts", len(accounts)))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("getting dev accounts: %w", err)
	}
	devAccount := eth.MustNewAddress(accounts[0])
	zlog.Info("dev account retrieved", zap.Stringer("dev_account", devAccount))

	// Create test keys
	zlog.Debug("generating test keys")
	deployerKey, err := eth.NewRandomPrivateKey()
	if err != nil {
		zlog.Error("failed to generate deployer key", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("generating deployer key: %w", err)
	}
	deployerAddr := deployerKey.PublicKey().Address()
	zlog.Debug("generated deployer key", zap.Stringer("deployer_address", deployerAddr))

	serviceProviderKey, err := eth.NewRandomPrivateKey()
	if err != nil {
		zlog.Error("failed to generate service provider key", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("generating service provider key: %w", err)
	}
	serviceProviderAddr := serviceProviderKey.PublicKey().Address()
	zlog.Debug("generated service provider key", zap.Stringer("service_provider_address", serviceProviderAddr))

	payerKey, err := eth.NewRandomPrivateKey()
	if err != nil {
		zlog.Error("failed to generate payer key", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("generating payer key: %w", err)
	}
	payerAddr := payerKey.PublicKey().Address()
	zlog.Debug("generated payer key", zap.Stringer("payer_address", payerAddr))

	// Fund deployer from dev account (10 ETH)
	zlog.Debug("funding deployer account")
	fundAmount := new(big.Int)
	fundAmount.SetString("10000000000000000000", 10) // 10 ETH
	if err := fundFromDevAccount(ctx, rpcURL, devAccount, deployerAddr, fundAmount); err != nil {
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
	if err := fundFromDevAccount(ctx, rpcURL, devAccount, payerAddr, fundAmount2); err != nil {
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
	if err := fundFromDevAccount(ctx, rpcURL, devAccount, serviceProviderAddr, fundAmount3); err != nil {
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
	grtAddr, err := deployContract(ctx, rpcURL, deployerKey, chainID, grtArtifact, nil)
	if err != nil {
		zlog.Error("failed to deploy GRT token", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying GRT: %w", err)
	}
	zlog.Info("MockGRTToken deployed", zap.Stringer("address", grtAddr))

	// 2. Deploy MockController
	controllerArtifact, err := loadContractArtifact("MockController")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading Controller artifact: %w", err)
	}
	controllerArgs, err := encodeConstructorArgs([]interface{}{deployerAddr})
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("encoding controller args: %w", err)
	}
	controllerAddr, err := deployContract(ctx, rpcURL, deployerKey, chainID, controllerArtifact, controllerArgs)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying Controller: %w", err)
	}
	zlog.Info("MockController deployed", zap.Stringer("address", controllerAddr))

	// 3. Deploy MockStaking
	stakingArtifact, err := loadContractArtifact("MockStaking")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading Staking artifact: %w", err)
	}
	stakingAddr, err := deployContract(ctx, rpcURL, deployerKey, chainID, stakingArtifact, nil)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying Staking: %w", err)
	}
	zlog.Info("MockStaking deployed", zap.Stringer("address", stakingAddr))

	// Load Staking ABI for setGraphToken call
	stakingABI, err := loadABI("MockStaking")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading Staking ABI: %w", err)
	}

	// Set GRT token in MockStaking (needed for addToDelegationPool and stakeTo)
	if err := callSetGraphToken(ctx, rpcURL, deployerKey, chainID, stakingAddr, grtAddr, stakingABI); err != nil {
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
	epochManagerAddr, err := deployContract(ctx, rpcURL, deployerKey, chainID, epochManagerArtifact, nil)
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
	rewardsManagerAddr, err := deployContract(ctx, rpcURL, deployerKey, chainID, rewardsManagerArtifact, nil)
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
	tokenGatewayAddr, err := deployContract(ctx, rpcURL, deployerKey, chainID, tokenGatewayArtifact, nil)
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
	proxyAdminAddr, err := deployContract(ctx, rpcURL, deployerKey, chainID, proxyAdminArtifact, nil)
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
	curationAddr, err := deployContract(ctx, rpcURL, deployerKey, chainID, curationArtifact, nil)
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

	// Load Controller ABI for setContractProxy calls
	controllerABI, err := loadABI("MockController")
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading Controller ABI: %w", err)
	}

	// We need placeholder addresses for GraphPayments and PaymentsEscrow
	// Use deployer address as placeholder - will be overwritten
	placeholderAddr := deployerAddr

	// Register all GraphDirectory dependencies
	registrations := []struct {
		name string
		addr eth.Address
	}{
		{"GraphToken", grtAddr},
		{"Staking", stakingAddr},
		{"HorizonStaking", stakingAddr},
		{"EpochManager", epochManagerAddr},
		{"RewardsManager", rewardsManagerAddr},
		{"GraphTokenGateway", tokenGatewayAddr},
		{"GraphProxyAdmin", proxyAdminAddr},
		{"Curation", curationAddr},
		{"GraphPayments", placeholderAddr},  // Placeholder - will be overwritten
		{"PaymentsEscrow", placeholderAddr}, // Placeholder - will be overwritten
	}

	for _, reg := range registrations {
		if err := callSetContractProxy(ctx, rpcURL, deployerKey, chainID, controllerAddr, reg.name, reg.addr, controllerABI); err != nil {
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
	graphPaymentsArgs, err := encodeConstructorArgs([]interface{}{controllerAddr, protocolCut})
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("encoding GraphPayments args: %w", err)
	}
	graphPaymentsAddr, err := deployContract(ctx, rpcURL, deployerKey, chainID, graphPaymentsArtifact, graphPaymentsArgs)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying GraphPayments: %w", err)
	}
	zlog.Info("ORIGINAL GraphPayments deployed", zap.Stringer("address", graphPaymentsAddr))

	// NOTE: We don't call initialize() because:
	// 1. The constructor calls _disableInitializers() (designed for proxy patterns)
	// 2. We don't need Multicall functionality for our tests
	// 3. The contract works correctly without initialization

	// Update Controller with real GraphPayments address
	if err := callSetContractProxy(ctx, rpcURL, deployerKey, chainID, controllerAddr, "GraphPayments", graphPaymentsAddr, controllerABI); err != nil {
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
	escrowArgs, err := encodeConstructorArgs([]interface{}{controllerAddr, thawingPeriod})
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("encoding PaymentsEscrow args: %w", err)
	}
	escrowAddr, err := deployContract(ctx, rpcURL, deployerKey, chainID, escrowArtifact, escrowArgs)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying PaymentsEscrow: %w", err)
	}
	zlog.Info("ORIGINAL PaymentsEscrow deployed", zap.Stringer("address", escrowAddr))

	// NOTE: We don't call initialize() because:
	// 1. The constructor calls _disableInitializers() (designed for proxy patterns)
	// 2. We don't need Multicall functionality for our tests
	// 3. The contract works correctly without initialization

	// Update Controller with real PaymentsEscrow address
	if err := callSetContractProxy(ctx, rpcURL, deployerKey, chainID, controllerAddr, "PaymentsEscrow", escrowAddr, controllerABI); err != nil {
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
	collectorArgs, err := encodeCollectorConstructorArgs("GraphTallyCollector", "1", controllerAddr, uint64(0))
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("encoding collector args: %w", err)
	}
	collectorAddr, err := deployContract(ctx, rpcURL, deployerKey, chainID, collectorArtifact, collectorArgs)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying Collector: %w", err)
	}
	zlog.Info("ORIGINAL GraphTallyCollector deployed", zap.Stringer("address", collectorAddr))

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
	dataServiceArgs, err := encodeConstructorArgs([]interface{}{controllerAddr, collectorAddr})
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("encoding SubstreamsDataService args: %w", err)
	}
	dataServiceContractAddr, err := deployContract(ctx, rpcURL, deployerKey, chainID, dataServiceArtifact, dataServiceArgs)
	if err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying SubstreamsDataService: %w", err)
	}
	zlog.Info("SubstreamsDataService deployed", zap.Stringer("address", dataServiceContractAddr))

	fmt.Printf("\n")
	fmt.Printf("============================================================\n")
	fmt.Printf("Test environment ready - using ORIGINAL Graph Protocol contracts\n")
	fmt.Printf("============================================================\n")
	fmt.Printf("  RPC URL: %s\n", rpcURL)
	fmt.Printf("  Chain ID: %d\n", chainID)
	fmt.Printf("  Deployer: %s\n", deployerAddr.Pretty())
	fmt.Printf("\n")
	fmt.Printf("ORIGINAL CONTRACTS (from horizon-contracts):\n")
	fmt.Printf("  GraphPayments: %s\n", graphPaymentsAddr.Pretty())
	fmt.Printf("  PaymentsEscrow: %s\n", escrowAddr.Pretty())
	fmt.Printf("  GraphTallyCollector: %s\n", collectorAddr.Pretty())
	fmt.Printf("  SubstreamsDataService: %s\n", dataServiceContractAddr.Pretty())
	fmt.Printf("\n")
	fmt.Printf("MOCK CONTRACTS (test infrastructure):\n")
	fmt.Printf("  MockGRTToken: %s\n", grtAddr.Pretty())
	fmt.Printf("  MockController: %s\n", controllerAddr.Pretty())
	fmt.Printf("  MockStaking: %s\n", stakingAddr.Pretty())
	fmt.Printf("\n")
	fmt.Printf("TEST ACCOUNTS:\n")
	fmt.Printf("  Service Provider: %s\n", serviceProviderAddr.Pretty())
	fmt.Printf("  Payer: %s\n", payerAddr.Pretty())
	fmt.Printf("============================================================\n")

	// Load all ABIs for contract interactions
	abis, err := loadAllABIs()
	if err != nil {
		zlog.Error("failed to load ABIs", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading ABIs: %w", err)
	}
	zlog.Info("contract ABIs loaded successfully")

	return &TestEnv{
		ctx:                   ctx,
		cancel:                cancel,
		anvilContainer:        anvilContainer,
		rpcURL:                rpcURL,
		ChainID:               chainID,
		GRTToken:              grtAddr,
		Controller:            controllerAddr,
		Staking:               stakingAddr,
		PaymentsEscrow:        escrowAddr,
		GraphPayments:         graphPaymentsAddr,
		CollectorAddress:      collectorAddr,
		SubstreamsDataService: dataServiceContractAddr,
		DeployerKey:           deployerKey,
		DeployerAddress:       deployerAddr,
		ServiceProviderKey:    serviceProviderKey,
		ServiceProviderAddr:   serviceProviderAddr,
		PayerKey:              payerKey,
		PayerAddr:             payerAddr,
		ABIs:                  abis,
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

// loadAllABIs loads all contract ABIs needed for testing
func loadAllABIs() (*ABIs, error) {
	grtABI, err := loadABI("MockGRTToken")
	if err != nil {
		return nil, fmt.Errorf("loading GRT ABI: %w", err)
	}

	stakingABI, err := loadABI("MockStaking")
	if err != nil {
		return nil, fmt.Errorf("loading Staking ABI: %w", err)
	}

	// Use ORIGINAL PaymentsEscrow ABI (not mock)
	escrowABI, err := loadABI("PaymentsEscrow")
	if err != nil {
		return nil, fmt.Errorf("loading PaymentsEscrow ABI: %w", err)
	}

	// Use ORIGINAL GraphTallyCollector ABI (not mock)
	collectorABI, err := loadABI("GraphTallyCollector")
	if err != nil {
		return nil, fmt.Errorf("loading Collector ABI: %w", err)
	}

	dataServiceABI, err := loadABI("SubstreamsDataService")
	if err != nil {
		return nil, fmt.Errorf("loading SubstreamsDataService ABI: %w", err)
	}

	controllerABI, err := loadABI("MockController")
	if err != nil {
		return nil, fmt.Errorf("loading Controller ABI: %w", err)
	}

	return &ABIs{
		GRTToken:    grtABI,
		Staking:     stakingABI,
		Escrow:      escrowABI,
		Collector:   collectorABI,
		DataService: dataServiceABI,
		Controller:  controllerABI,
	}, nil
}

func fundFromDevAccount(ctx context.Context, rpcURL string, from, to eth.Address, amount *big.Int) error {
	params := []interface{}{
		map[string]interface{}{
			"from":  from.Pretty(),
			"to":    to.Pretty(),
			"value": fmt.Sprintf("0x%x", amount),
		},
	}

	txHash, err := rpcCall[string](ctx, rpcURL, "eth_sendTransaction", params)
	if err != nil {
		return fmt.Errorf("sending fund transaction: %w", err)
	}

	return waitForReceipt(ctx, rpcURL, txHash)
}

func deployContract(ctx context.Context, rpcURL string, key *eth.PrivateKey, chainID uint64, artifact *ContractArtifact, constructorArgs []byte) (eth.Address, error) {
	bytecode := artifact.Bytecode.Object
	if strings.HasPrefix(bytecode, "0x") {
		bytecode = bytecode[2:]
	}

	deployerAddr := key.PublicKey().Address()
	zlog.Debug("deploying contract from address", zap.Stringer("deployer", deployerAddr), zap.Uint64("chain_id", chainID))

	// Get nonce
	nonceHex, err := rpcCall[string](ctx, rpcURL, "eth_getTransactionCount", []interface{}{deployerAddr.Pretty(), "latest"})
	if err != nil {
		zlog.Error("failed to get nonce for contract deployment", zap.Error(err), zap.Stringer("deployer", deployerAddr))
		return eth.Address{}, fmt.Errorf("getting nonce: %w", err)
	}
	nonce, _ := new(big.Int).SetString(nonceHex[2:], 16)
	zlog.Debug("got nonce for deployment", zap.Uint64("nonce", nonce.Uint64()))

	// Get gas price
	gasPriceHex, err := rpcCall[string](ctx, rpcURL, "eth_gasPrice", nil)
	if err != nil {
		return eth.Address{}, fmt.Errorf("getting gas price: %w", err)
	}
	gasPrice, _ := new(big.Int).SetString(gasPriceHex[2:], 16)

	// Create raw transaction for contract deployment
	// Gas limit estimate for contract deployment
	gasLimit := uint64(5000000) // Increased for larger original contracts

	bytecodeBytes, err := hex.DecodeString(bytecode)
	if err != nil {
		return eth.Address{}, fmt.Errorf("decoding bytecode: %w", err)
	}

	// Append constructor args if provided
	data := bytecodeBytes
	if constructorArgs != nil {
		data = append(data, constructorArgs...)
	}

	// Create EIP-155 transaction
	tx := createLegacyTx(nonce.Uint64(), nil, big.NewInt(0), gasLimit, gasPrice, data)

	// Sign transaction
	zlog.Debug("signing deployment transaction", zap.Uint64("chain_id", chainID))
	signedTx, err := signLegacyTx(tx, chainID, key)
	if err != nil {
		zlog.Error("failed to sign deployment transaction", zap.Error(err), zap.Uint64("chain_id", chainID))
		return eth.Address{}, fmt.Errorf("signing transaction: %w", err)
	}

	// Send raw transaction
	zlog.Debug("sending deployment transaction")
	txHash, err := rpcCall[string](ctx, rpcURL, "eth_sendRawTransaction", []interface{}{"0x" + hex.EncodeToString(signedTx)})
	if err != nil {
		zlog.Error("failed to send deployment transaction", zap.Error(err))
		return eth.Address{}, fmt.Errorf("sending transaction: %w", err)
	}
	zlog.Debug("deployment transaction sent", zap.String("tx_hash", txHash))

	// Wait for receipt
	if err := waitForReceipt(ctx, rpcURL, txHash); err != nil {
		zlog.Error("failed to get receipt for deployment transaction", zap.Error(err), zap.String("tx_hash", txHash))
		return eth.Address{}, fmt.Errorf("waiting for receipt: %w", err)
	}

	// Get receipt to find contract address
	receipt, err := rpcCall[map[string]interface{}](ctx, rpcURL, "eth_getTransactionReceipt", []interface{}{txHash})
	if err != nil {
		zlog.Error("failed to get receipt", zap.Error(err), zap.String("tx_hash", txHash))
		return eth.Address{}, fmt.Errorf("getting receipt: %w", err)
	}

	contractAddrStr, ok := receipt["contractAddress"].(string)
	if !ok || contractAddrStr == "" {
		zlog.Error("contract address not found in receipt", zap.String("tx_hash", txHash))
		return eth.Address{}, fmt.Errorf("contract address not in receipt")
	}

	contractAddr := eth.MustNewAddress(contractAddrStr)
	zlog.Debug("contract deployed successfully", zap.Stringer("contract_address", contractAddr), zap.String("tx_hash", txHash))
	return contractAddr, nil
}

func waitForReceipt(ctx context.Context, rpcURL, txHash string) error {
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for transaction %s", txHash)
		case <-ticker.C:
			receipt, err := rpcCall[map[string]interface{}](ctx, rpcURL, "eth_getTransactionReceipt", []interface{}{txHash})
			if err != nil || receipt == nil {
				continue // Not mined yet
			}
			statusStr, _ := receipt["status"].(string)
			if statusStr == "0x0" {
				return fmt.Errorf("transaction failed: %s", txHash)
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// rpcCall makes a JSON-RPC call
func rpcCall[T any](ctx context.Context, rpcURL, method string, params interface{}) (T, error) {
	var result T

	reqBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
		"id":      1,
	}
	if params == nil {
		reqBody["params"] = []interface{}{}
	}

	bodyBytes, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, "POST", rpcURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()

	var rpcResp struct {
		Result T         `json:"result"`
		Error  *rpcError `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return result, err
	}

	if rpcResp.Error != nil {
		if rpcResp.Error.Data != "" {
			return result, fmt.Errorf("RPC error %d: %s (data: %s)", rpcResp.Error.Code, rpcResp.Error.Message, rpcResp.Error.Data)
		}
		return result, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

// CallContract makes a contract call
func (env *TestEnv) CallContract(to eth.Address, data []byte) ([]byte, error) {
	params := []interface{}{
		map[string]interface{}{
			"to":   to.Pretty(),
			"data": "0x" + hex.EncodeToString(data),
		},
		"latest",
	}

	resultHex, err := rpcCall[string](env.ctx, env.rpcURL, "eth_call", params)
	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(resultHex, "0x") {
		resultHex = resultHex[2:]
	}

	return hex.DecodeString(resultHex)
}

// Legacy transaction RLP encoding
func createLegacyTx(nonce uint64, to *eth.Address, value *big.Int, gasLimit uint64, gasPrice *big.Int, data []byte) []interface{} {
	tx := make([]interface{}, 9)
	tx[0] = nonce
	tx[1] = gasPrice
	tx[2] = gasLimit
	if to != nil {
		// Explicitly convert to []byte to ensure proper type for RLP encoding
		addrBytes := make([]byte, 20)
		copy(addrBytes, (*to)[:])
		tx[3] = addrBytes
	} else {
		tx[3] = []byte{} // Contract creation
	}
	tx[4] = value
	tx[5] = data
	// v, r, s will be filled after signing
	return tx
}

func signLegacyTx(tx []interface{}, chainID uint64, key *eth.PrivateKey) ([]byte, error) {
	zlog.Debug("signLegacyTx called", zap.Uint64("chain_id", chainID))

	// Prepare transaction for signing (EIP-155)
	// Hash: keccak256(rlp(nonce, gasPrice, gasLimit, to, value, data, chainId, 0, 0))
	txForSigning := make([]interface{}, 9)
	copy(txForSigning, tx[:6])
	txForSigning[6] = chainID
	txForSigning[7] = uint64(0)
	txForSigning[8] = uint64(0)

	zlog.Debug("prepared transaction for signing", zap.Uint64("chain_id_in_tx", chainID))

	signingHash := eth.Keccak256(rlpEncode(txForSigning))
	zlog.Debug("computed signing hash", zap.String("hash", hex.EncodeToString(signingHash)))

	sig, err := key.Sign(signingHash)
	if err != nil {
		zlog.Error("failed to sign hash", zap.Error(err))
		return nil, err
	}

	// Extract r, s, v from signature
	// eth-go Signature is [V (1 byte), R (32 bytes), S (32 bytes)] format
	// V is typically 27 or 28 (Ethereum standard)
	v := uint64(sig[0])
	r := new(big.Int).SetBytes(sig[1:33])
	s := new(big.Int).SetBytes(sig[33:65])

	zlog.Debug("extracted signature components", zap.Uint64("v_raw", v), zap.String("r", r.String()), zap.String("s", s.String()))

	// Normalize v to raw ECDSA recovery ID (0 or 1)
	// eth-go Sign() returns v = 27 or 28 (Ethereum standard)
	if v >= 27 {
		v -= 27
	}

	// EIP-155: v = v + chainId * 2 + 35
	v = v + chainID*2 + 35

	tx[6] = v
	tx[7] = r
	tx[8] = s

	encoded := rlpEncode(tx)
	return encoded, nil
}

// rlpEncode encodes data in RLP format
func rlpEncode(items []interface{}) []byte {
	var buf bytes.Buffer

	for _, item := range items {
		encodeItem(&buf, item)
	}

	// If total length > 55, use long list encoding
	content := buf.Bytes()
	if len(content) <= 55 {
		result := make([]byte, 1+len(content))
		result[0] = 0xc0 + byte(len(content))
		copy(result[1:], content)
		return result
	}

	// Long list
	lenBytes := encodeLength(uint64(len(content)))
	result := make([]byte, 1+len(lenBytes)+len(content))
	result[0] = 0xf7 + byte(len(lenBytes))
	copy(result[1:], lenBytes)
	copy(result[1+len(lenBytes):], content)
	return result
}

func encodeItem(buf *bytes.Buffer, item interface{}) {
	switch v := item.(type) {
	case []byte:
		if len(v) == 0 {
			buf.WriteByte(0x80)
		} else if len(v) == 1 && v[0] < 0x80 {
			buf.WriteByte(v[0])
		} else if len(v) <= 55 {
			buf.WriteByte(0x80 + byte(len(v)))
			buf.Write(v)
		} else {
			lenBytes := encodeLength(uint64(len(v)))
			buf.WriteByte(0xb7 + byte(len(lenBytes)))
			buf.Write(lenBytes)
			buf.Write(v)
		}
	case uint64:
		if v == 0 {
			buf.WriteByte(0x80)
		} else if v < 0x80 {
			buf.WriteByte(byte(v))
		} else {
			b := big.NewInt(int64(v)).Bytes()
			buf.WriteByte(0x80 + byte(len(b)))
			buf.Write(b)
		}
	case *big.Int:
		if v == nil || v.Sign() == 0 {
			buf.WriteByte(0x80)
		} else {
			b := v.Bytes()
			if len(b) == 1 && b[0] < 0x80 {
				buf.WriteByte(b[0])
			} else if len(b) <= 55 {
				buf.WriteByte(0x80 + byte(len(b)))
				buf.Write(b)
			} else {
				lenBytes := encodeLength(uint64(len(b)))
				buf.WriteByte(0xb7 + byte(len(lenBytes)))
				buf.Write(lenBytes)
				buf.Write(b)
			}
		}
	}
}

func encodeLength(length uint64) []byte {
	if length < 256 {
		return []byte{byte(length)}
	}
	b := big.NewInt(int64(length)).Bytes()
	return b
}

// Cleanup terminates the test environment
func (env *TestEnv) Cleanup() {
	if env.anvilContainer != nil {
		env.anvilContainer.Terminate(env.ctx)
	}
	env.cancel()
}

// encodeConstructorArgs encodes constructor arguments for contract deployment
// Supports: address, uint256, uint64, string
func encodeConstructorArgs(args []interface{}) ([]byte, error) {
	var encoded []byte
	for _, arg := range args {
		switch v := arg.(type) {
		case eth.Address:
			// Pad address to 32 bytes (left-padded with zeros)
			padded := make([]byte, 32)
			copy(padded[12:], v[:])
			encoded = append(encoded, padded...)
		case *big.Int:
			// Pad big.Int to 32 bytes
			padded := make([]byte, 32)
			vBytes := v.Bytes()
			copy(padded[32-len(vBytes):], vBytes)
			encoded = append(encoded, padded...)
		case uint64:
			// Pad uint64 to 32 bytes
			padded := make([]byte, 32)
			binary.BigEndian.PutUint64(padded[24:], v)
			encoded = append(encoded, padded...)
		case string:
			// For strings, we need to encode: offset, length, data
			// This is a simplified version - full ABI encoding is more complex
			return nil, fmt.Errorf("string encoding not yet supported in simplified encoder")
		default:
			return nil, fmt.Errorf("unsupported argument type: %T", arg)
		}
	}
	return encoded, nil
}

// encodeCollectorConstructorArgs encodes GraphTallyCollectorFull constructor args
// Constructor: (string eip712Name, string eip712Version, address controller, uint256 revokeSignerThawingPeriod)
func encodeCollectorConstructorArgs(name, version string, controller eth.Address, thawingPeriod uint64) ([]byte, error) {
	// This requires dynamic ABI encoding for strings
	// Offset layout:
	// [0x00-0x1f]: offset to name (128 = 0x80)
	// [0x20-0x3f]: offset to version (192 = 0xc0)
	// [0x40-0x5f]: controller address (padded)
	// [0x60-0x7f]: thawingPeriod (uint256)
	// [0x80-...]: name string (length + data)
	// [0xc0-...]: version string (length + data)

	buf := make([]byte, 0, 512)

	// Calculate offsets
	nameOffset := uint64(128) // 4 * 32
	versionOffsetBase := nameOffset + 32 + uint64(((len(name)+31)/32)*32)

	// Offset to name
	offsetBytes := make([]byte, 32)
	binary.BigEndian.PutUint64(offsetBytes[24:], nameOffset)
	buf = append(buf, offsetBytes...)

	// Offset to version
	offsetBytes = make([]byte, 32)
	binary.BigEndian.PutUint64(offsetBytes[24:], versionOffsetBase)
	buf = append(buf, offsetBytes...)

	// Controller address
	addrBytes := make([]byte, 32)
	copy(addrBytes[12:], controller[:])
	buf = append(buf, addrBytes...)

	// Thawing period
	thawingBytes := make([]byte, 32)
	binary.BigEndian.PutUint64(thawingBytes[24:], thawingPeriod)
	buf = append(buf, thawingBytes...)

	// Name string
	nameLen := make([]byte, 32)
	binary.BigEndian.PutUint64(nameLen[24:], uint64(len(name)))
	buf = append(buf, nameLen...)
	namePadded := make([]byte, ((len(name)+31)/32)*32)
	copy(namePadded, name)
	buf = append(buf, namePadded...)

	// Version string
	versionLen := make([]byte, 32)
	binary.BigEndian.PutUint64(versionLen[24:], uint64(len(version)))
	buf = append(buf, versionLen...)
	versionPadded := make([]byte, ((len(version)+31)/32)*32)
	copy(versionPadded, version)
	buf = append(buf, versionPadded...)

	return buf, nil
}

// callSetContractProxy calls Controller.setContractProxy(bytes32 id, address contractAddress)
func callSetContractProxy(ctx context.Context, rpcURL string, key *eth.PrivateKey, chainID uint64, controllerAddr eth.Address, name string, contractAddr eth.Address, controllerABI *eth.ABI) error {
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

	return sendTransaction(ctx, rpcURL, key, chainID, &controllerAddr, big.NewInt(0), data)
}

// callSetGraphToken calls MockStaking.setGraphToken(address token)
func callSetGraphToken(ctx context.Context, rpcURL string, key *eth.PrivateKey, chainID uint64, stakingAddr eth.Address, tokenAddr eth.Address, stakingABI *eth.ABI) error {
	setGraphTokenFn := stakingABI.FindFunctionByName("setGraphToken")
	if setGraphTokenFn == nil {
		return fmt.Errorf("setGraphToken function not found in ABI")
	}

	data, err := setGraphTokenFn.NewCall(tokenAddr).Encode()
	if err != nil {
		return fmt.Errorf("encoding setGraphToken call: %w", err)
	}

	return sendTransaction(ctx, rpcURL, key, chainID, &stakingAddr, big.NewInt(0), data)
}

// sendTransaction sends a transaction and waits for receipt
func sendTransaction(ctx context.Context, rpcURL string, key *eth.PrivateKey, chainID uint64, to *eth.Address, value *big.Int, data []byte) error {
	from := key.PublicKey().Address()

	toStr := "contract_creation"
	if to != nil {
		toStr = to.Pretty()
	}
	zlog.Debug("sending transaction", zap.Stringer("from", from), zap.String("to", toStr), zap.Uint64("chain_id", chainID))

	// Get nonce
	nonceHex, err := rpcCall[string](ctx, rpcURL, "eth_getTransactionCount", []interface{}{from.Pretty(), "latest"})
	if err != nil {
		zlog.Error("failed to get nonce", zap.Error(err), zap.Stringer("from", from))
		return fmt.Errorf("getting nonce: %w", err)
	}
	nonce, _ := new(big.Int).SetString(nonceHex[2:], 16)
	zlog.Debug("got nonce", zap.Uint64("nonce", nonce.Uint64()))

	// Get gas price
	gasPriceHex, err := rpcCall[string](ctx, rpcURL, "eth_gasPrice", nil)
	if err != nil {
		return fmt.Errorf("getting gas price: %w", err)
	}
	gasPrice, _ := new(big.Int).SetString(gasPriceHex[2:], 16)

	gasLimit := uint64(500000)

	// Create transaction
	tx := createLegacyTx(nonce.Uint64(), to, value, gasLimit, gasPrice, data)

	// Sign
	zlog.Debug("signing transaction", zap.Uint64("chain_id", chainID))
	signedTx, err := signLegacyTx(tx, chainID, key)
	if err != nil {
		zlog.Error("failed to sign transaction", zap.Error(err), zap.Uint64("chain_id", chainID))
		return fmt.Errorf("signing transaction: %w", err)
	}

	// Send
	zlog.Debug("submitting transaction to RPC")
	txHash, err := rpcCall[string](ctx, rpcURL, "eth_sendRawTransaction", []interface{}{"0x" + hex.EncodeToString(signedTx)})
	if err != nil {
		zlog.Error("failed to send transaction", zap.Error(err))
		return fmt.Errorf("sending transaction: %w", err)
	}
	zlog.Debug("transaction submitted", zap.String("tx_hash", txHash))

	err = waitForReceipt(ctx, rpcURL, txHash)
	if err != nil {
		zlog.Error("transaction failed", zap.Error(err), zap.String("tx_hash", txHash))
	} else {
		zlog.Debug("transaction confirmed", zap.String("tx_hash", txHash))
	}
	return err
}
