package devenv

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"sync"
	"time"

	"github.com/streamingfast/eth-go"
	"github.com/streamingfast/eth-go/rpc"
	"github.com/streamingfast/logging"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"
)

var zlog, _ = logging.PackageLogger("devenv", "github.com/graphprotocol/substreams-data-service/horizon/devenv")

// Env holds the development environment state
type Env struct {
	ctx            context.Context
	cancel         context.CancelFunc
	anvilContainer testcontainers.Container
	rpcClient      *rpc.Client
	RPCURL         string
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
	User1           Account
	User2           Account
	User3           Account
}

var (
	globalEnv     *Env
	globalEnvOnce sync.Once
	globalEnvErr  error
)

// Start starts the development environment (singleton)
// Returns the existing environment if already started
func Start(ctx context.Context, opts ...Option) (*Env, error) {
	globalEnvOnce.Do(func() {
		globalEnv, globalEnvErr = start(ctx, opts...)
	})
	return globalEnv, globalEnvErr
}

// Get returns the current environment or nil if not started
func Get() *Env {
	return globalEnv
}

// Shutdown shuts down the development environment
func Shutdown() {
	if globalEnv != nil {
		globalEnv.cleanup()
		globalEnv = nil
		globalEnvOnce = sync.Once{}
	}
}

// cleanup terminates the environment
func (env *Env) cleanup() {
	if env.anvilContainer != nil {
		env.anvilContainer.Terminate(env.ctx)
	}
	env.cancel()
}

func start(ctx context.Context, opts ...Option) (*Env, error) {
	config := DefaultConfig()
	for _, opt := range opts {
		opt(config)
	}

	report := config.Reporter.ReportProgress

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)

	zlog.Info("starting development environment")

	// Pre-load all contracts (ABIs loaded now, addresses set after deployment)
	report("Loading contract ABIs...")
	grtToken := mustLoadContract("MockGRTToken")
	controller := mustLoadContract("MockController")
	staking := mustLoadContract("MockStaking")
	escrow := mustLoadContract("PaymentsEscrow")
	graphPayments := mustLoadContract("GraphPayments")
	collector := mustLoadContract("GraphTallyCollector")
	dataService := mustLoadContract("SubstreamsDataService")

	// Start Anvil container
	report("Starting Anvil container...")
	anvilReq := testcontainers.ContainerRequest{
		Image: "ghcr.io/foundry-rs/foundry:latest",
		Cmd: []string{
			fmt.Sprintf("anvil --host 0.0.0.0 --port 8545 --chain-id %d --block-time %d", config.ChainID, config.BlockTime),
		},
		ExposedPorts: []string{"8545/tcp"},
		WaitingFor: wait.ForListeningPort("8545/tcp").
			WithStartupTimeout(60 * time.Second),
	}

	anvilContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: anvilReq,
		Started:          true,
	})
	if err != nil {
		zlog.Error("failed to start Anvil container", zap.Error(err))
		cancel()
		return nil, fmt.Errorf("starting anvil container: %w", err)
	}

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
	report("Waiting for Anvil RPC to be ready...")
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
	accounts, err := rpc.Do[[]string](rpcClient, ctx, "eth_accounts", nil)
	if err != nil || len(accounts) == 0 {
		zlog.Error("failed to get dev accounts", zap.Error(err), zap.Int("num_accounts", len(accounts)))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("getting dev accounts: %w", err)
	}
	devAccount := eth.MustNewAddress(accounts[0])

	// Create test accounts (deterministic keys for reproducibility)
	report("Creating test accounts...")
	deployer := mustAccountFromHex("1aa5d8f9a42ba0b9439c7034d24e93619f67af22a9ab15be9e4ce7eadddb5143")
	serviceProvider := mustAccountFromHex("41942233cf1d78b6e3262f1806f8da36aafa24a941031aad8e056a1d34640f8d")
	payer := mustAccountFromHex("e4c2694501255921b6588519cfd36d4e86ddc4ce19ab1bc91d9c58057c040304")
	user1 := mustAccountFromHex("dd02564c0e9836fb570322be23f8355761d4d04ebccdc53f4f53325227680a9f")
	user2 := mustAccountFromHex("bc3def46fab7929038dfb0df7e0168cba60d3384aceabf85e23e5e0ff90c8fe3")
	user3 := mustAccountFromHex("7acd0f26d5be968f73ca8f2198fa52cc595650f8d5819ee9122fe90329847c48")

	// Fund all test accounts from dev account (10 ETH each)
	report("Funding test accounts...")
	fundAmount := new(big.Int)
	fundAmount.SetString("10000000000000000000", 10) // 10 ETH

	for name, addr := range map[string]eth.Address{
		"deployer":         deployer.Address,
		"service_provider": serviceProvider.Address,
		"payer":            payer.Address,
		"user1":            user1.Address,
		"user2":            user2.Address,
		"user3":            user3.Address,
	} {
		if err := fundFromDevAccount(ctx, rpcClient, devAccount, addr, fundAmount); err != nil {
			zlog.Error("failed to fund account", zap.String("name", name), zap.Error(err))
			anvilContainer.Terminate(ctx)
			cancel()
			return nil, fmt.Errorf("funding %s: %w", name, err)
		}
	}

	chainID := chainIDInt.Uint64()

	// Deploy all contracts
	report("Deploying contracts...")
	if err := deployAllContracts(ctx, rpcClient, chainID, deployer, grtToken, controller, staking, escrow, graphPayments, collector, dataService); err != nil {
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, err
	}

	env := &Env{
		ctx:             ctx,
		cancel:          cancel,
		anvilContainer:  anvilContainer,
		rpcClient:       rpcClient,
		RPCURL:          rpcURL,
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
		User1:           user1,
		User2:           user2,
		User3:           user3,
	}

	// Mint GRT to all test accounts
	report("Minting GRT to test accounts...")
	for name, addr := range map[string]eth.Address{
		"deployer":         deployer.Address,
		"service_provider": serviceProvider.Address,
		"payer":            payer.Address,
		"user1":            user1.Address,
		"user2":            user2.Address,
		"user3":            user3.Address,
	} {
		if err := env.MintGRT(addr, config.EscrowAmount); err != nil {
			env.cleanup()
			return nil, fmt.Errorf("minting GRT to %s: %w", name, err)
		}
	}

	report("Development environment ready")

	return env, nil
}

func deployAllContracts(ctx context.Context, rpcClient *rpc.Client, chainID uint64, deployer Account, grtToken, controller, staking, escrow, graphPayments, collector, dataService *Contract) error {
	// ============================================================================
	// PHASE 1: Deploy all MOCK infrastructure contracts
	// ============================================================================
	zlog.Info("Phase 1: Deploying mock infrastructure contracts")

	// 1. Deploy MockGRTToken
	grtArtifact, err := loadContractArtifact("MockGRTToken")
	if err != nil {
		return fmt.Errorf("loading GRT artifact: %w", err)
	}
	grtToken.Address, err = deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, grtArtifact, nil)
	if err != nil {
		return fmt.Errorf("deploying GRT: %w", err)
	}
	zlog.Info("MockGRTToken deployed", zap.Stringer("address", grtToken.Address))

	// 2. Deploy MockController
	controllerArtifact, err := loadContractArtifact("MockController")
	if err != nil {
		return fmt.Errorf("loading Controller artifact: %w", err)
	}
	controller.Address, err = deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, controllerArtifact, controller.ABI, deployer.Address)
	if err != nil {
		return fmt.Errorf("deploying Controller: %w", err)
	}
	zlog.Info("MockController deployed", zap.Stringer("address", controller.Address))

	// 3. Deploy MockStaking
	stakingArtifact, err := loadContractArtifact("MockStaking")
	if err != nil {
		return fmt.Errorf("loading Staking artifact: %w", err)
	}
	staking.Address, err = deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, stakingArtifact, nil)
	if err != nil {
		return fmt.Errorf("deploying Staking: %w", err)
	}
	zlog.Info("MockStaking deployed", zap.Stringer("address", staking.Address))

	// Set GRT token in MockStaking
	if err := callSetGraphToken(ctx, rpcClient, deployer.PrivateKey, chainID, staking.Address, grtToken.Address, staking.ABI); err != nil {
		return fmt.Errorf("setting GRT token in staking: %w", err)
	}

	// 4-8. Deploy other mock contracts
	epochManagerArtifact, err := loadContractArtifact("MockEpochManager")
	if err != nil {
		return fmt.Errorf("loading EpochManager artifact: %w", err)
	}
	epochManagerAddr, err := deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, epochManagerArtifact, nil)
	if err != nil {
		return fmt.Errorf("deploying EpochManager: %w", err)
	}
	zlog.Info("MockEpochManager deployed", zap.Stringer("address", epochManagerAddr))

	rewardsManagerArtifact, err := loadContractArtifact("MockRewardsManager")
	if err != nil {
		return fmt.Errorf("loading RewardsManager artifact: %w", err)
	}
	rewardsManagerAddr, err := deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, rewardsManagerArtifact, nil)
	if err != nil {
		return fmt.Errorf("deploying RewardsManager: %w", err)
	}
	zlog.Info("MockRewardsManager deployed", zap.Stringer("address", rewardsManagerAddr))

	tokenGatewayArtifact, err := loadContractArtifact("MockTokenGateway")
	if err != nil {
		return fmt.Errorf("loading TokenGateway artifact: %w", err)
	}
	tokenGatewayAddr, err := deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, tokenGatewayArtifact, nil)
	if err != nil {
		return fmt.Errorf("deploying TokenGateway: %w", err)
	}
	zlog.Info("MockTokenGateway deployed", zap.Stringer("address", tokenGatewayAddr))

	proxyAdminArtifact, err := loadContractArtifact("MockProxyAdmin")
	if err != nil {
		return fmt.Errorf("loading ProxyAdmin artifact: %w", err)
	}
	proxyAdminAddr, err := deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, proxyAdminArtifact, nil)
	if err != nil {
		return fmt.Errorf("deploying ProxyAdmin: %w", err)
	}
	zlog.Info("MockProxyAdmin deployed", zap.Stringer("address", proxyAdminAddr))

	curationArtifact, err := loadContractArtifact("MockCuration")
	if err != nil {
		return fmt.Errorf("loading Curation artifact: %w", err)
	}
	curationAddr, err := deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, curationArtifact, nil)
	if err != nil {
		return fmt.Errorf("deploying Curation: %w", err)
	}
	zlog.Info("MockCuration deployed", zap.Stringer("address", curationAddr))

	// ============================================================================
	// PHASE 2: Register ALL contracts in Controller with PLACEHOLDER addresses
	// ============================================================================
	zlog.Info("Phase 2: Registering contracts in Controller")

	placeholderAddr := deployer.Address

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
		{"GraphPayments", placeholderAddr},
		{"PaymentsEscrow", placeholderAddr},
	}

	for _, reg := range registrations {
		if err := callSetContractProxy(ctx, rpcClient, deployer.PrivateKey, chainID, controller.Address, reg.name, reg.addr, controller.ABI); err != nil {
			return fmt.Errorf("registering %s in controller: %w", reg.name, err)
		}
		zlog.Debug("registered contract in Controller", zap.String("name", reg.name), zap.Stringer("address", reg.addr))
	}

	// ============================================================================
	// PHASE 3: Deploy ORIGINAL GraphPayments contract
	// ============================================================================
	zlog.Info("Phase 3: Deploying original GraphPayments")

	graphPaymentsArtifact, err := loadContractArtifact("GraphPayments")
	if err != nil {
		return fmt.Errorf("loading GraphPayments artifact: %w", err)
	}
	protocolCut := big.NewInt(10000) // 1% protocol cut
	graphPayments.Address, err = deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, graphPaymentsArtifact, graphPayments.ABI, controller.Address, protocolCut)
	if err != nil {
		return fmt.Errorf("deploying GraphPayments: %w", err)
	}
	zlog.Info("ORIGINAL GraphPayments deployed", zap.Stringer("address", graphPayments.Address))

	if err := callSetContractProxy(ctx, rpcClient, deployer.PrivateKey, chainID, controller.Address, "GraphPayments", graphPayments.Address, controller.ABI); err != nil {
		return fmt.Errorf("updating GraphPayments in controller: %w", err)
	}

	// ============================================================================
	// PHASE 4: Deploy ORIGINAL PaymentsEscrow contract
	// ============================================================================
	zlog.Info("Phase 4: Deploying original PaymentsEscrow")

	escrowArtifact, err := loadContractArtifact("PaymentsEscrow")
	if err != nil {
		return fmt.Errorf("loading PaymentsEscrow artifact: %w", err)
	}
	thawingPeriod := big.NewInt(0)
	escrow.Address, err = deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, escrowArtifact, escrow.ABI, controller.Address, thawingPeriod)
	if err != nil {
		return fmt.Errorf("deploying PaymentsEscrow: %w", err)
	}
	zlog.Info("ORIGINAL PaymentsEscrow deployed", zap.Stringer("address", escrow.Address))

	if err := callSetContractProxy(ctx, rpcClient, deployer.PrivateKey, chainID, controller.Address, "PaymentsEscrow", escrow.Address, controller.ABI); err != nil {
		return fmt.Errorf("updating PaymentsEscrow in controller: %w", err)
	}

	// ============================================================================
	// PHASE 5: Deploy ORIGINAL GraphTallyCollector
	// ============================================================================
	zlog.Info("Phase 5: Deploying original GraphTallyCollector")

	collectorArtifact, err := loadContractArtifact("GraphTallyCollector")
	if err != nil {
		return fmt.Errorf("loading Collector artifact: %w", err)
	}
	collector.Address, err = deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, collectorArtifact, collector.ABI, "GraphTallyCollector", "1", controller.Address, big.NewInt(0))
	if err != nil {
		return fmt.Errorf("deploying Collector: %w", err)
	}
	zlog.Info("ORIGINAL GraphTallyCollector deployed", zap.Stringer("address", collector.Address))

	// ============================================================================
	// PHASE 6: Deploy SubstreamsDataService contract
	// ============================================================================
	zlog.Info("Phase 6: Deploying SubstreamsDataService")

	dataServiceArtifact, err := loadContractArtifact("SubstreamsDataService")
	if err != nil {
		return fmt.Errorf("loading SubstreamsDataService artifact: %w", err)
	}
	dataService.Address, err = deployContract(ctx, rpcClient, deployer.PrivateKey, chainID, dataServiceArtifact, dataService.ABI, controller.Address, collector.Address)
	if err != nil {
		return fmt.Errorf("deploying SubstreamsDataService: %w", err)
	}
	zlog.Info("SubstreamsDataService deployed", zap.Stringer("address", dataService.Address))

	return nil
}

// callSetContractProxy calls Controller.setContractProxy
func callSetContractProxy(ctx context.Context, rpcClient *rpc.Client, key *eth.PrivateKey, chainID uint64, controllerAddr eth.Address, name string, contractAddr eth.Address, controllerABI *eth.ABI) error {
	setContractProxyFn := controllerABI.FindFunctionByName("setContractProxy")
	if setContractProxyFn == nil {
		return fmt.Errorf("setContractProxy function not found in ABI")
	}

	nameHash := eth.Keccak256([]byte(name))

	data, err := setContractProxyFn.NewCall(nameHash, contractAddr).Encode()
	if err != nil {
		return fmt.Errorf("encoding setContractProxy call: %w", err)
	}

	return SendTransaction(ctx, rpcClient, key, chainID, &controllerAddr, big.NewInt(0), data)
}

// callSetGraphToken calls MockStaking.setGraphToken
func callSetGraphToken(ctx context.Context, rpcClient *rpc.Client, key *eth.PrivateKey, chainID uint64, stakingAddr eth.Address, tokenAddr eth.Address, stakingABI *eth.ABI) error {
	setGraphTokenFn := stakingABI.FindFunctionByName("setGraphToken")
	if setGraphTokenFn == nil {
		return fmt.Errorf("setGraphToken function not found in ABI")
	}

	data, err := setGraphTokenFn.NewCall(tokenAddr).Encode()
	if err != nil {
		return fmt.Errorf("encoding setGraphToken call: %w", err)
	}

	return SendTransaction(ctx, rpcClient, key, chainID, &stakingAddr, big.NewInt(0), data)
}

// PrintInfo prints the environment information to the given writer
func (env *Env) PrintInfo(w io.Writer) {
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "============================================================\n")
	fmt.Fprintf(w, "  Development Environment Ready\n")
	fmt.Fprintf(w, "============================================================\n")
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "NETWORK:\n")
	fmt.Fprintf(w, "  RPC URL:  %s\n", env.RPCURL)
	fmt.Fprintf(w, "  Chain ID: %d\n", env.ChainID)
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "CONTRACTS:\n")
	fmt.Fprintf(w, "  GraphPayments:         %s\n", env.GraphPayments.Address.Pretty())
	fmt.Fprintf(w, "  PaymentsEscrow:        %s\n", env.Escrow.Address.Pretty())
	fmt.Fprintf(w, "  GraphTallyCollector:   %s\n", env.Collector.Address.Pretty())
	fmt.Fprintf(w, "  SubstreamsDataService: %s\n", env.DataService.Address.Pretty())
	fmt.Fprintf(w, "  MockGRTToken:          %s\n", env.GRTToken.Address.Pretty())
	fmt.Fprintf(w, "  MockController:        %s\n", env.Controller.Address.Pretty())
	fmt.Fprintf(w, "  MockStaking:           %s\n", env.Staking.Address.Pretty())
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "TEST ACCOUNTS (10 ETH + 10,000 GRT each):\n")
	fmt.Fprintf(w, "  Deployer:         %s (0x%s)\n", env.Deployer.Address.Pretty(), env.Deployer.PrivateKey.String())
	fmt.Fprintf(w, "  Service Provider: %s (0x%s)\n", env.ServiceProvider.Address.Pretty(), env.ServiceProvider.PrivateKey.String())
	fmt.Fprintf(w, "  Payer:            %s (0x%s)\n", env.Payer.Address.Pretty(), env.Payer.PrivateKey.String())
	fmt.Fprintf(w, "  User1:            %s (0x%s)\n", env.User1.Address.Pretty(), env.User1.PrivateKey.String())
	fmt.Fprintf(w, "  User2:            %s (0x%s)\n", env.User2.Address.Pretty(), env.User2.PrivateKey.String())
	fmt.Fprintf(w, "  User3:            %s (0x%s)\n", env.User3.Address.Pretty(), env.User3.PrivateKey.String())
	fmt.Fprintf(w, "\n")
	fmt.Fprintf(w, "============================================================\n")
}
