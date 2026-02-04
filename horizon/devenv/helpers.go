package devenv

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/graphprotocol/substreams-data-service/horizon"
	"github.com/streamingfast/eth-go"
	"github.com/streamingfast/eth-go/rpc"
	"github.com/streamingfast/eth-go/signer/native"
	"go.uber.org/zap"
)

// waitForReceipt waits for a transaction receipt
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

// SendTransaction sends a transaction and waits for receipt
func SendTransaction(ctx context.Context, rpcClient *rpc.Client, key *eth.PrivateKey, chainID uint64, to *eth.Address, value *big.Int, data []byte) error {
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

// CallContract makes a read-only contract call
func (env *Env) CallContract(to eth.Address, data []byte) ([]byte, error) {
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

// MintGRT mints GRT tokens to an address
func (env *Env) MintGRT(to eth.Address, amount *big.Int) error {
	data, err := env.GRTToken.CallData("mint", to, amount)
	if err != nil {
		return err
	}
	return SendTransaction(env.ctx, env.rpcClient, env.Deployer.PrivateKey, env.ChainID, &env.GRTToken.Address, big.NewInt(0), data)
}

// ApproveGRT approves the escrow contract to spend GRT (from Payer account)
func (env *Env) ApproveGRT(amount *big.Int) error {
	data, err := env.GRTToken.CallData("approve", env.Escrow.Address, amount)
	if err != nil {
		return err
	}
	return SendTransaction(env.ctx, env.rpcClient, env.Payer.PrivateKey, env.ChainID, &env.GRTToken.Address, big.NewInt(0), data)
}

// DepositEscrow deposits GRT into escrow (from Payer to Collector for ServiceProvider)
func (env *Env) DepositEscrow(amount *big.Int) error {
	data, err := env.Escrow.CallData("deposit", env.Collector.Address, env.ServiceProvider.Address, amount)
	if err != nil {
		return err
	}
	return SendTransaction(env.ctx, env.rpcClient, env.Payer.PrivateKey, env.ChainID, &env.Escrow.Address, big.NewInt(0), data)
}

// SetProvision sets provision tokens for service provider
func (env *Env) SetProvision(tokens *big.Int, maxVerifierCut uint32, thawingPeriod uint64) error {
	data, err := env.Staking.CallData("setProvision", env.ServiceProvider.Address, env.DataService.Address, tokens, maxVerifierCut, thawingPeriod)
	if err != nil {
		return err
	}
	return SendTransaction(env.ctx, env.rpcClient, env.Deployer.PrivateKey, env.ChainID, &env.Staking.Address, big.NewInt(0), data)
}

// SetProvisionTokensRange sets the minimum provision tokens for the data service
func (env *Env) SetProvisionTokensRange(minimumProvisionTokens *big.Int) error {
	data, err := env.DataService.CallData("setProvisionTokensRange", minimumProvisionTokens)
	if err != nil {
		return err
	}
	return SendTransaction(env.ctx, env.rpcClient, env.Deployer.PrivateKey, env.ChainID, &env.DataService.Address, big.NewInt(0), data)
}

// RegisterServiceProvider registers the service provider with the data service
func (env *Env) RegisterServiceProvider() error {
	// Encode the paymentsDestination as the data parameter (abi.encode(address))
	registerData := make([]byte, 32)
	copy(registerData[12:], env.ServiceProvider.Address[:])

	data, err := env.DataService.CallData("register", env.ServiceProvider.Address, registerData)
	if err != nil {
		return err
	}
	return SendTransaction(env.ctx, env.rpcClient, env.ServiceProvider.PrivateKey, env.ChainID, &env.DataService.Address, big.NewInt(0), data)
}

// AuthorizeSigner authorizes a signer key to sign RAVs for the payer
func (env *Env) AuthorizeSigner(signerKey *eth.PrivateKey) error {
	signerAddr := signerKey.PublicKey().Address()

	// Generate proof with deadline 1 hour in the future
	proofDeadline := uint64(time.Now().Add(1 * time.Hour).Unix())

	proof, err := GenerateSignerProof(env.ChainID, env.Collector.Address, proofDeadline, env.Payer.Address, signerKey)
	if err != nil {
		return fmt.Errorf("generating signer proof: %w", err)
	}

	// Encode call: authorizeSigner(address signer, uint256 proofDeadline, bytes proof)
	data, err := env.Collector.CallData("authorizeSigner", signerAddr, new(big.Int).SetUint64(proofDeadline), proof)
	if err != nil {
		return fmt.Errorf("encoding authorizeSigner call: %w", err)
	}

	return SendTransaction(env.ctx, env.rpcClient, env.Payer.PrivateKey, env.ChainID, &env.Collector.Address, big.NewInt(0), data)
}

// ThawSigner initiates thawing for a signer
func (env *Env) ThawSigner(signer eth.Address) error {
	data, err := env.Collector.CallData("thawSigner", signer)
	if err != nil {
		return fmt.Errorf("encoding thawSigner call: %w", err)
	}
	return SendTransaction(env.ctx, env.rpcClient, env.Payer.PrivateKey, env.ChainID, &env.Collector.Address, big.NewInt(0), data)
}

// RevokeAuthorizedSigner revokes a signer after thawing
func (env *Env) RevokeAuthorizedSigner(signer eth.Address) error {
	data, err := env.Collector.CallData("revokeAuthorizedSigner", signer)
	if err != nil {
		return fmt.Errorf("encoding revokeAuthorizedSigner call: %w", err)
	}
	return SendTransaction(env.ctx, env.rpcClient, env.Payer.PrivateKey, env.ChainID, &env.Collector.Address, big.NewInt(0), data)
}

// RevokeSigner performs the two-step revoke flow: thaw + revoke
func (env *Env) RevokeSigner(signer eth.Address) error {
	if err := env.ThawSigner(signer); err != nil {
		return fmt.Errorf("thawing signer: %w", err)
	}
	if err := env.RevokeAuthorizedSigner(signer); err != nil {
		return fmt.Errorf("revoking signer: %w", err)
	}
	return nil
}

// IsAuthorized checks if a signer is authorized for an authorizer
func (env *Env) IsAuthorized(authorizer eth.Address, signer eth.Address) (bool, error) {
	data, err := env.Collector.CallData("isAuthorized", authorizer, signer)
	if err != nil {
		return false, fmt.Errorf("encoding isAuthorized call: %w", err)
	}

	result, err := env.CallContract(env.Collector.Address, data)
	if err != nil {
		return false, err
	}

	// Result is bool (32 bytes, last byte is 0 or 1)
	return result[31] == 1, nil
}

// Domain returns an EIP-712 domain for the collector contract
func (env *Env) Domain() *horizon.Domain {
	return horizon.NewDomain(env.ChainID, env.Collector.Address)
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
func (env *Env) SetupTestWithSigner(config *TestSetupConfig) (*TestSetupResult, error) {
	if config == nil {
		config = DefaultTestSetupConfig()
	}

	// Mint GRT to payer
	if err := env.MintGRT(env.Payer.Address, config.EscrowAmount); err != nil {
		return nil, fmt.Errorf("minting GRT: %w", err)
	}

	// Approve escrow to spend GRT
	if err := env.ApproveGRT(config.EscrowAmount); err != nil {
		return nil, fmt.Errorf("approving GRT: %w", err)
	}

	// Deposit to escrow
	if err := env.DepositEscrow(config.EscrowAmount); err != nil {
		return nil, fmt.Errorf("depositing to escrow: %w", err)
	}

	// Set provision tokens range (min = 0 for testing)
	if err := env.SetProvisionTokensRange(big.NewInt(0)); err != nil {
		return nil, fmt.Errorf("setting provision tokens range: %w", err)
	}

	// Set provision for service provider
	if err := env.SetProvision(config.ProvisionAmount, 0, 0); err != nil {
		return nil, fmt.Errorf("setting provision: %w", err)
	}

	// Register service provider with data service
	if err := env.RegisterServiceProvider(); err != nil {
		return nil, fmt.Errorf("registering with data service: %w", err)
	}

	// Create and authorize signer
	signerKey, err := eth.NewRandomPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("creating signer key: %w", err)
	}

	if err := env.AuthorizeSigner(signerKey); err != nil {
		return nil, fmt.Errorf("authorizing signer: %w", err)
	}

	return &TestSetupResult{
		SignerKey:  signerKey,
		SignerAddr: signerKey.PublicKey().Address(),
	}, nil
}

// MustNewCollectionID creates a CollectionID from a hex string or panics
func MustNewCollectionID(hexStr string) horizon.CollectionID {
	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash(hexStr)[:])
	return collectionID
}

// GetEscrowBalance returns the escrow balance for a payer -> receiver via collector
func (env *Env) GetEscrowBalance(payer, receiver eth.Address) (*big.Int, error) {
	data, err := env.Escrow.CallData("getBalance", payer, env.Collector.Address, receiver)
	if err != nil {
		return nil, fmt.Errorf("encoding getBalance call: %w", err)
	}

	result, err := env.CallContract(env.Escrow.Address, data)
	if err != nil {
		return nil, fmt.Errorf("calling getBalance: %w", err)
	}

	// Result is uint256 (32 bytes)
	if len(result) != 32 {
		return nil, fmt.Errorf("unexpected result length: %d", len(result))
	}

	return new(big.Int).SetBytes(result), nil
}
