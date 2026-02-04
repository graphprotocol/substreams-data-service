package devenv

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/graphprotocol/substreams-data-service/horizon/devenv/contracts"
	"github.com/streamingfast/eth-go"
	"github.com/streamingfast/eth-go/rpc"
	"github.com/streamingfast/eth-go/signer/native"
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

// mustLoadContract loads a contract ABI from embedded artifact and returns a Contract with zero address
func mustLoadContract(name string) *Contract {
	artifact, err := loadContractArtifact(name)
	if err != nil {
		panic(fmt.Sprintf("loading %s artifact: %v", name, err))
	}

	abi, err := eth.ParseABIFromBytes(artifact.ABI)
	if err != nil {
		panic(fmt.Sprintf("parsing %s ABI: %v", name, err))
	}

	return &Contract{ABI: abi}
}

// loadContractArtifact loads a contract artifact (ABI and bytecode) from embedded JSON
func loadContractArtifact(name string) (*ContractArtifact, error) {
	data, err := contracts.FS.ReadFile(name + ".json")
	if err != nil {
		return nil, fmt.Errorf("reading embedded artifact: %w", err)
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
