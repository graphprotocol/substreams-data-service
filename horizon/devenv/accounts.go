package devenv

import (
	"context"
	"fmt"
	"math/big"

	"github.com/streamingfast/eth-go"
	"github.com/streamingfast/eth-go/rpc"
)

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

// fundFromDevAccount funds an account from the Anvil dev account (uses eth_sendTransaction)
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
