package devenv

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/streamingfast/eth-go"
	"github.com/streamingfast/eth-go/rpc"
	"github.com/streamingfast/eth-go/signer/native"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/zap"
)

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

// ContractArtifact represents a compiled Foundry contract
type ContractArtifact struct {
	ABI      json.RawMessage `json:"abi"`
	Bytecode struct {
		Object string `json:"object"`
	} `json:"bytecode"`
}

// mustLoadContract loads a contract ABI from artifact and returns a Contract with zero address
func mustLoadContract(name string) *Contract {
	abi, err := loadABI(name)
	if err != nil {
		panic(fmt.Sprintf("loading %s ABI: %v", name, err))
	}
	return &Contract{ABI: abi}
}

// loadABI loads an ABI from a Foundry contract artifact JSON file
func loadABI(name string) (*eth.ABI, error) {
	artifactPath := filepath.Join(getContractsDir(), name+".json")
	return eth.ParseABI(artifactPath)
}

// loadContractArtifact loads a contract artifact (ABI and bytecode) from JSON
func loadContractArtifact(name string) (*ContractArtifact, error) {
	artifactPath := filepath.Join(getContractsDir(), name+".json")

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

// deployContract deploys a contract and returns its address
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

// ensureContractArtifacts checks if contract artifacts exist, builds them if needed
func ensureContractArtifacts(forceBuild bool) error {
	artifactsDir := getContractsDir()
	collectorArtifact := filepath.Join(artifactsDir, "GraphTallyCollector.json")

	// Check if artifacts already exist
	if !forceBuild {
		if _, err := os.Stat(collectorArtifact); err == nil {
			zlog.Info("contract artifacts found, skipping build")
			return nil
		}
	}

	zlog.Info("building contract artifacts...")

	// Ensure output directory exists
	if err := os.MkdirAll(artifactsDir, 0755); err != nil {
		return fmt.Errorf("creating artifacts directory: %w", err)
	}

	// Run build container
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	buildDir := getBuildDir()

	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:       buildDir,
			Dockerfile:    "Dockerfile",
			PrintBuildLog: true,
		},
		Mounts: testcontainers.ContainerMounts{
			testcontainers.BindMount(artifactsDir, "/output"),
		},
		WaitingFor: wait.ForLog("Build complete!").
			WithStartupTimeout(5 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return fmt.Errorf("starting build container: %w", err)
	}
	defer container.Terminate(ctx)

	// Wait for container to complete by checking state
	for {
		state, err := container.State(ctx)
		if err != nil {
			return fmt.Errorf("getting container state: %w", err)
		}

		if !state.Running {
			if state.ExitCode != 0 {
				return fmt.Errorf("build container exited with code %d", state.ExitCode)
			}
			break
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for build container")
		case <-time.After(1 * time.Second):
			continue
		}
	}

	// Verify artifact was created
	if _, err := os.Stat(collectorArtifact); err != nil {
		return fmt.Errorf("artifact not found after build: %w", err)
	}

	zlog.Info("contract artifacts built successfully")
	return nil
}

// getDevenvDir returns the absolute path to the devenv directory
func getDevenvDir() string {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to get current file path")
	}
	return filepath.Dir(currentFile)
}

// getBuildDir returns the path to the build directory
func getBuildDir() string {
	return filepath.Join(getDevenvDir(), "build")
}

// getContractsDir returns the path to the contracts artifacts directory
func getContractsDir() string {
	return filepath.Join(filepath.Dir(getDevenvDir()), "testdata", "contracts")
}
