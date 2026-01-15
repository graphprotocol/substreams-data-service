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
	GRTToken  *eth.ABI
	Staking   *eth.ABI
	Escrow    *eth.ABI
	Collector *eth.ABI
}

// TestEnv holds the test environment state
type TestEnv struct {
	ctx                 context.Context
	cancel              context.CancelFunc
	anvilContainer      testcontainers.Container
	rpcURL              string
	ChainID             uint64
	GRTToken            eth.Address
	Controller          eth.Address
	Staking             eth.Address
	PaymentsEscrow      eth.Address
	CollectorAddress    eth.Address
	DeployerKey         *eth.PrivateKey
	DeployerAddress     eth.Address
	ServiceProviderKey  *eth.PrivateKey
	ServiceProviderAddr eth.Address
	PayerKey            *eth.PrivateKey
	PayerAddr           eth.Address
	DataServiceKey      *eth.PrivateKey
	DataServiceAddr     eth.Address

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
	zlog.Info("Geth RPC endpoint ready", zap.String("rpc_url", rpcURL))

	// Wait for RPC to be responsive and get the ACTUAL chain ID
	// Geth dev mode assigns a random chain ID, we must use whatever it returns
	zlog.Info("querying chain ID from Geth node")
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

	// Get dev account (funded by Geth --dev)
	zlog.Debug("querying dev accounts")
	accounts, err := rpcCall[[]string](ctx, rpcURL, "eth_accounts", nil)
	if err != nil || len(accounts) == 0 {
		zlog.Error("failed to get dev accounts", zap.Error(err), zap.Int("num_accounts", len(accounts)))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("getting dev accounts: %w", err)
	}
	devAccount := eth.MustNewAddress(accounts[0])
	zlog.Info("dev account retrieved", zap.String("dev_account", devAccount.Pretty()))

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
	zlog.Debug("generated deployer key", zap.String("deployer_address", deployerAddr.Pretty()))

	serviceProviderKey, err := eth.NewRandomPrivateKey()
	if err != nil {
		zlog.Error("failed to generate service provider key", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("generating service provider key: %w", err)
	}
	serviceProviderAddr := serviceProviderKey.PublicKey().Address()
	zlog.Debug("generated service provider key", zap.String("service_provider_address", serviceProviderAddr.Pretty()))

	payerKey, err := eth.NewRandomPrivateKey()
	if err != nil {
		zlog.Error("failed to generate payer key", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("generating payer key: %w", err)
	}
	payerAddr := payerKey.PublicKey().Address()
	zlog.Debug("generated payer key", zap.String("payer_address", payerAddr.Pretty()))

	dataServiceKey, err := eth.NewRandomPrivateKey()
	if err != nil {
		zlog.Error("failed to generate data service key", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("generating data service key: %w", err)
	}
	dataServiceAddr := dataServiceKey.PublicKey().Address()
	zlog.Debug("generated data service key", zap.String("data_service_address", dataServiceAddr.Pretty()))

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

	// Fund data service account (2 ETH for gas)
	zlog.Debug("funding data service account")
	fundAmount3 := new(big.Int)
	fundAmount3.SetString("2000000000000000000", 10) // 2 ETH
	if err := fundFromDevAccount(ctx, rpcURL, devAccount, dataServiceAddr, fundAmount3); err != nil {
		zlog.Error("failed to fund data service", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("funding data service: %w", err)
	}
	zlog.Debug("data service funded successfully", zap.String("amount", "2 ETH"))

	// Deploy contract stack in order: GRT -> Controller -> Staking -> Escrow -> Collector
	zlog.Info("deploying contract stack", zap.Uint64("chain_id", chainIDInt.Uint64()))

	// 1. Deploy GRT Token
	zlog.Debug("loading GRT token artifact")
	grtArtifact, err := loadContractArtifact("MockGRTToken")
	if err != nil {
		zlog.Error("failed to load GRT artifact", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading GRT artifact: %w", err)
	}
	zlog.Debug("deploying GRT token contract", zap.Uint64("chain_id", chainIDInt.Uint64()))
	grtAddr, err := deployContract(ctx, rpcURL, deployerKey, chainIDInt.Uint64(), grtArtifact, nil)
	if err != nil {
		zlog.Error("failed to deploy GRT token", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying GRT: %w", err)
	}
	zlog.Info("GRT token deployed", zap.String("grt_address", grtAddr.Pretty()))

	// 2. Deploy Controller
	zlog.Debug("loading Controller artifact")
	controllerArtifact, err := loadContractArtifact("MockController")
	if err != nil {
		zlog.Error("failed to load Controller artifact", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading Controller artifact: %w", err)
	}
	// Controller constructor takes governor address
	controllerArgs, err := encodeConstructorArgs([]interface{}{deployerAddr})
	if err != nil {
		zlog.Error("failed to encode controller args", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("encoding controller args: %w", err)
	}
	zlog.Debug("deploying Controller contract", zap.Uint64("chain_id", chainIDInt.Uint64()))
	controllerAddr, err := deployContract(ctx, rpcURL, deployerKey, chainIDInt.Uint64(), controllerArtifact, controllerArgs)
	if err != nil {
		zlog.Error("failed to deploy Controller", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying Controller: %w", err)
	}
	zlog.Info("Controller deployed", zap.String("controller_address", controllerAddr.Pretty()))

	// 3. Deploy Staking
	zlog.Debug("loading Staking artifact")
	stakingArtifact, err := loadContractArtifact("MockStaking")
	if err != nil {
		zlog.Error("failed to load Staking artifact", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading Staking artifact: %w", err)
	}
	zlog.Debug("deploying Staking contract", zap.Uint64("chain_id", chainIDInt.Uint64()))
	stakingAddr, err := deployContract(ctx, rpcURL, deployerKey, chainIDInt.Uint64(), stakingArtifact, nil)
	if err != nil {
		zlog.Error("failed to deploy Staking", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying Staking: %w", err)
	}
	zlog.Info("Staking deployed", zap.String("staking_address", stakingAddr.Pretty()))

	// 4. Deploy PaymentsEscrow
	zlog.Debug("loading PaymentsEscrow artifact")
	escrowArtifact, err := loadContractArtifact("MockPaymentsEscrow")
	if err != nil {
		zlog.Error("failed to load PaymentsEscrow artifact", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading PaymentsEscrow artifact: %w", err)
	}
	// PaymentsEscrow constructor takes GRT token address
	escrowArgs, err := encodeConstructorArgs([]interface{}{grtAddr})
	if err != nil {
		zlog.Error("failed to encode escrow args", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("encoding escrow args: %w", err)
	}
	zlog.Debug("deploying PaymentsEscrow contract", zap.Uint64("chain_id", chainIDInt.Uint64()))
	escrowAddr, err := deployContract(ctx, rpcURL, deployerKey, chainIDInt.Uint64(), escrowArtifact, escrowArgs)
	if err != nil {
		zlog.Error("failed to deploy PaymentsEscrow", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying PaymentsEscrow: %w", err)
	}
	zlog.Info("PaymentsEscrow deployed", zap.String("escrow_address", escrowAddr.Pretty()))

	// 5. Register contracts in Controller
	zlog.Debug("registering contracts in Controller")
	if err := callSetContractProxy(ctx, rpcURL, deployerKey, chainIDInt.Uint64(), controllerAddr, "GraphToken", grtAddr); err != nil {
		zlog.Error("failed to register GRT in controller", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("registering GRT in controller: %w", err)
	}
	zlog.Debug("registered GraphToken in Controller")
	if err := callSetContractProxy(ctx, rpcURL, deployerKey, chainIDInt.Uint64(), controllerAddr, "HorizonStaking", stakingAddr); err != nil {
		zlog.Error("failed to register Staking in controller", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("registering Staking in controller: %w", err)
	}
	zlog.Debug("registered HorizonStaking in Controller")
	if err := callSetContractProxy(ctx, rpcURL, deployerKey, chainIDInt.Uint64(), controllerAddr, "PaymentsEscrow", escrowAddr); err != nil {
		zlog.Error("failed to register Escrow in controller", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("registering Escrow in controller: %w", err)
	}
	zlog.Debug("registered PaymentsEscrow in Controller")

	// 6. Deploy GraphTallyCollectorFull
	zlog.Debug("loading GraphTallyCollectorFull artifact")
	collectorArtifact, err := loadContractArtifact("GraphTallyCollectorFull")
	if err != nil {
		zlog.Error("failed to load Collector artifact", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("loading Collector artifact: %w", err)
	}
	// Constructor: (string eip712Name, string eip712Version, address controller, uint256 revokeSignerThawingPeriod)
	collectorArgs, err := encodeCollectorConstructorArgs("GraphTallyCollector", "1", controllerAddr, uint64(0))
	if err != nil {
		zlog.Error("failed to encode collector args", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("encoding collector args: %w", err)
	}
	zlog.Debug("deploying GraphTallyCollectorFull contract", zap.Uint64("chain_id", chainIDInt.Uint64()))
	collectorAddr, err := deployContract(ctx, rpcURL, deployerKey, chainIDInt.Uint64(), collectorArtifact, collectorArgs)
	if err != nil {
		zlog.Error("failed to deploy Collector", zap.Error(err))
		anvilContainer.Terminate(ctx)
		cancel()
		return nil, fmt.Errorf("deploying Collector: %w", err)
	}
	zlog.Info("GraphTallyCollectorFull deployed", zap.String("collector_address", collectorAddr.Pretty()))

	fmt.Printf("Test environment ready:\n")
	fmt.Printf("  RPC URL: %s\n", rpcURL)
	fmt.Printf("  Chain ID: %d\n", chainIDInt.Uint64())
	fmt.Printf("  Deployer: %s\n", deployerAddr.Pretty())
	fmt.Printf("  GRT Token: %s\n", grtAddr.Pretty())
	fmt.Printf("  Controller: %s\n", controllerAddr.Pretty())
	fmt.Printf("  Staking: %s\n", stakingAddr.Pretty())
	fmt.Printf("  PaymentsEscrow: %s\n", escrowAddr.Pretty())
	fmt.Printf("  Collector: %s\n", collectorAddr.Pretty())
	fmt.Printf("  Service Provider: %s\n", serviceProviderAddr.Pretty())
	fmt.Printf("  Payer: %s\n", payerAddr.Pretty())
	fmt.Printf("  Data Service: %s\n", dataServiceAddr.Pretty())

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
		ctx:                 ctx,
		cancel:              cancel,
		anvilContainer:      anvilContainer,
		rpcURL:              rpcURL,
		ChainID:             chainIDInt.Uint64(),
		GRTToken:            grtAddr,
		Controller:          controllerAddr,
		Staking:             stakingAddr,
		PaymentsEscrow:      escrowAddr,
		CollectorAddress:    collectorAddr,
		DeployerKey:         deployerKey,
		DeployerAddress:     deployerAddr,
		ServiceProviderKey:  serviceProviderKey,
		ServiceProviderAddr: serviceProviderAddr,
		PayerKey:            payerKey,
		PayerAddr:           payerAddr,
		DataServiceKey:      dataServiceKey,
		DataServiceAddr:     dataServiceAddr,
		ABIs:                abis,
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

	escrowABI, err := loadABI("MockPaymentsEscrow")
	if err != nil {
		return nil, fmt.Errorf("loading Escrow ABI: %w", err)
	}

	collectorABI, err := loadABI("GraphTallyCollectorFull")
	if err != nil {
		return nil, fmt.Errorf("loading Collector ABI: %w", err)
	}

	return &ABIs{
		GRTToken:  grtABI,
		Staking:   stakingABI,
		Escrow:    escrowABI,
		Collector: collectorABI,
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
	zlog.Debug("deploying contract from address", zap.String("deployer", deployerAddr.Pretty()), zap.Uint64("chain_id", chainID))

	// Get nonce
	nonceHex, err := rpcCall[string](ctx, rpcURL, "eth_getTransactionCount", []interface{}{deployerAddr.Pretty(), "latest"})
	if err != nil {
		zlog.Error("failed to get nonce for contract deployment", zap.Error(err), zap.String("deployer", deployerAddr.Pretty()))
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
	gasLimit := uint64(3000000)

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
	zlog.Debug("contract deployed successfully", zap.String("contract_address", contractAddr.Pretty()), zap.String("tx_hash", txHash))
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
		return result, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return rpcResp.Result, nil
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
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

	fmt.Printf("DEBUG CallContract: to=%s data=%s\n", to.Pretty(), "0x"+hex.EncodeToString(data))

	resultHex, err := rpcCall[string](env.ctx, env.rpcURL, "eth_call", params)
	if err != nil {
		fmt.Printf("DEBUG CallContract error: %v\n", err)
		return nil, err
	}

	fmt.Printf("DEBUG CallContract result: %s\n", resultHex)

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

	fmt.Printf("DEBUG signLegacyTx: sig len=%d, v_raw=%d, chainID=%d\n", len(sig), v, chainID)
	zlog.Debug("extracted signature components", zap.Uint64("v_raw", v), zap.String("r", r.String()), zap.String("s", s.String()))

	// Normalize v to raw ECDSA recovery ID (0 or 1)
	// eth-go Sign() returns v = 27 or 28 (Ethereum standard)
	vOriginal := v
	if v >= 27 {
		v -= 27
	}
	fmt.Printf("DEBUG signLegacyTx: normalized v from %d to %d\n", vOriginal, v)
	zlog.Debug("normalized v to raw ECDSA recovery ID", zap.Uint64("v_original", vOriginal), zap.Uint64("v_normalized", v))

	// EIP-155: v = v + chainId * 2 + 35
	vBeforeAdjustment := v
	v = v + chainID*2 + 35
	calculatedChainID := (v - 35) / 2

	fmt.Printf("DEBUG signLegacyTx: v_normalized=%d, v_final=%d, calculated_chain_id=%d\n", vBeforeAdjustment, v, calculatedChainID)
	zlog.Debug("calculated EIP-155 v value",
		zap.Uint64("v_raw", vBeforeAdjustment),
		zap.Uint64("chain_id", chainID),
		zap.Uint64("v_final", v),
		zap.Uint64("expected_chain_id_from_v", calculatedChainID))

	tx[6] = v
	tx[7] = r
	tx[8] = s

	fmt.Printf("DEBUG signLegacyTx: tx contents before RLP:\n")
	for i, item := range tx {
		fmt.Printf("  tx[%d] type=%T value=%v\n", i, item, item)
	}

	encoded := rlpEncode(tx)
	printLen := len(encoded)
	if printLen > 100 {
		printLen = 100
	}
	fmt.Printf("DEBUG signLegacyTx: RLP encoded len=%d hex=%x\n", len(encoded), encoded[:printLen])
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
func callSetContractProxy(ctx context.Context, rpcURL string, key *eth.PrivateKey, chainID uint64, controllerAddr eth.Address, name string, contractAddr eth.Address) error {
	// Function selector: setContractProxy(bytes32,address) = 0xe0e99292
	selector, _ := hex.DecodeString("e0e99292")

	// Encode: keccak256(name) + padded address
	nameHash := eth.Keccak256([]byte(name))
	data := make([]byte, 4+32+32)
	copy(data[0:4], selector)
	copy(data[4:36], nameHash)
	copy(data[36+12:68], contractAddr[:])

	return sendTransaction(ctx, rpcURL, key, chainID, &controllerAddr, big.NewInt(0), data)
}

// sendTransaction sends a transaction and waits for receipt
func sendTransaction(ctx context.Context, rpcURL string, key *eth.PrivateKey, chainID uint64, to *eth.Address, value *big.Int, data []byte) error {
	from := key.PublicKey().Address()

	toStr := "contract_creation"
	if to != nil {
		toStr = to.Pretty()
	}
	zlog.Debug("sending transaction", zap.String("from", from.Pretty()), zap.String("to", toStr), zap.Uint64("chain_id", chainID))

	// Get nonce
	nonceHex, err := rpcCall[string](ctx, rpcURL, "eth_getTransactionCount", []interface{}{from.Pretty(), "latest"})
	if err != nil {
		zlog.Error("failed to get nonce", zap.Error(err), zap.String("from", from.Pretty()))
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
