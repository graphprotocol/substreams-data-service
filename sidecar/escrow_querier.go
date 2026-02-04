package sidecar

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/streamingfast/eth-go"
	"github.com/streamingfast/eth-go/rpc"
)

// EscrowQuerier provides methods to query the PaymentsEscrow contract
type EscrowQuerier struct {
	rpcClient  *rpc.Client
	escrowAddr eth.Address
}

// NewEscrowQuerier creates a new EscrowQuerier
func NewEscrowQuerier(rpcEndpoint string, escrowAddr eth.Address) *EscrowQuerier {
	return &EscrowQuerier{
		rpcClient:  rpc.NewClient(rpcEndpoint),
		escrowAddr: escrowAddr,
	}
}

// GetBalance returns the escrow balance for a payer -> receiver via collector
// This calls PaymentsEscrow.getBalance(payer, collector, receiver)
func (q *EscrowQuerier) GetBalance(ctx context.Context, payer, collector, receiver eth.Address) (*big.Int, error) {
	// Build the call data for getBalance(address,address,address)
	// Function selector: keccak256("getBalance(address,address,address)")[:4]
	// = 0xd6a58fd9
	selector := []byte{0xd6, 0xa5, 0x8f, 0xd9}

	// ABI encode the parameters (each address is 32 bytes, left-padded)
	data := make([]byte, 4+32*3)
	copy(data[:4], selector)
	copy(data[4+12:4+32], payer[:])
	copy(data[4+32+12:4+64], collector[:])
	copy(data[4+64+12:4+96], receiver[:])

	params := rpc.CallParams{
		To:   q.escrowAddr,
		Data: data,
	}

	resultHex, err := q.rpcClient.Call(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("calling getBalance: %w", err)
	}

	if strings.HasPrefix(resultHex, "0x") {
		resultHex = resultHex[2:]
	}

	resultBytes, err := hex.DecodeString(resultHex)
	if err != nil {
		return nil, fmt.Errorf("decoding result: %w", err)
	}

	if len(resultBytes) != 32 {
		return nil, fmt.Errorf("unexpected result length: %d", len(resultBytes))
	}

	return new(big.Int).SetBytes(resultBytes), nil
}
