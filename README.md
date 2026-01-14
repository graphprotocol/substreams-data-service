# Horizon - Golang RAV Implementation

A Golang implementation of Receipt Aggregated Vouchers (RAV) for The Graph's Timeline Aggregation Protocol (TAP) V2 (Horizon).

## Overview

This package provides the core functionality for creating, signing, and aggregating receipts into RAVs using EIP-712 typed data signing. It supports only V2 (Horizon) mode with the `GraphTallyCollector` contract.

## Features

- **Receipt Creation**: Create receipts with collection-based identifiers
- **EIP-712 Signing**: Sign receipts and RAVs using EIP-712 typed data
- **Signature Verification**: Recover signers from signatures
- **Receipt Aggregation**: Aggregate multiple receipts into a single RAV
- **Malleability Protection**: Signature normalization to prevent duplicate signatures
- **Comprehensive Validation**: Timestamp, signer, and field consistency checks

## Installation

```bash
go get github.com/streamingfast/horizon-go
```

## Quick Start

```go
package main

import (
	"fmt"
	"math/big"

	"github.com/streamingfast/eth-go"
	"github.com/streamingfast/horizon-go"
)

func main() {
	// Setup EIP-712 domain
	chainID := uint64(1)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := horizon.NewDomain(chainID, verifyingContract)

	// Generate keys
	senderKey, _ := eth.NewRandomPrivateKey()
	aggregatorKey, _ := eth.NewRandomPrivateKey()

	// Create a receipt
	var collectionID horizon.CollectionID
	receipt := horizon.NewReceipt(
		collectionID,
		senderKey.PublicKey().Address(),
		eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
		eth.MustNewAddress("0x3333333333333333333333333333333333333333"),
		big.NewInt(1000),
	)

	// Sign the receipt
	signedReceipt, _ := horizon.Sign(domain, receipt, senderKey)

	// Create aggregator with accepted signers
	aggregator := horizon.NewAggregator(
		domain,
		aggregatorKey,
		[]eth.Address{senderKey.PublicKey().Address()},
	)

	// Aggregate receipts into a RAV
	receipts := []*horizon.SignedReceipt{signedReceipt}
	signedRAV, _ := aggregator.AggregateReceipts(receipts, nil)

	fmt.Printf("RAV Value: %s\n", signedRAV.Message.ValueAggregate.String())
}
```

## Core Types

### Receipt

A V2 TAP receipt representing a payment:

```go
type Receipt struct {
    CollectionID    CollectionID  // Collection identifier
    Payer           eth.Address   // Payer address
    DataService     eth.Address   // Data service address
    ServiceProvider eth.Address   // Service provider address
    TimestampNs     uint64        // Unix timestamp in nanoseconds
    Nonce           uint64        // Random collision avoidance
    Value           *big.Int      // GRT value (uint128)
}
```

### RAV (Receipt Aggregate Voucher)

An aggregated voucher combining multiple receipts:

```go
type RAV struct {
    CollectionID    CollectionID  // Collection ID
    Payer           eth.Address   // Payer address
    ServiceProvider eth.Address   // Service provider address
    DataService     eth.Address   // Data service address
    TimestampNs     uint64        // Max timestamp from aggregated receipts
    ValueAggregate  *big.Int      // Total aggregated value (uint128)
    Metadata        []byte        // Extensible metadata
}
```

### SignedMessage

Generic wrapper for signed messages:

```go
type SignedMessage[T any] struct {
    Message   T             // The message (Receipt or RAV)
    Signature eth.Signature // EIP-712 signature
}
```

## API Reference

### Creating Receipts

```go
receipt := horizon.NewReceipt(
    collectionID,
    payer,
    dataService,
    serviceProvider,
    value,
)
```

### Signing Messages

```go
signedReceipt, err := horizon.Sign(domain, receipt, privateKey)
if err != nil {
    // Handle error
}
```

### Recovering Signers

```go
signer, err := signedReceipt.RecoverSigner(domain)
if err != nil {
    // Handle error
}
```

### Aggregating Receipts

```go
// Create aggregator with accepted signers
aggregator := horizon.NewAggregator(domain, signerKey, acceptedSigners)

// Aggregate receipts (with optional previous RAV)
signedRAV, err := aggregator.AggregateReceipts(receipts, previousRAV)
if err != nil {
    // Handle error
}
```

## Validation Rules

The aggregator enforces the following validation rules:

1. **Unique Signatures**: No duplicate signatures (includes malleability protection)
2. **Authorized Signers**: All receipts must be signed by accepted signers
3. **Timestamp Ordering**: Receipt timestamps must be greater than previous RAV timestamp
4. **Field Consistency**: All receipts must have matching collection ID, payer, data service, and service provider
5. **RAV Consistency**: Previous RAV fields must match receipt fields
6. **No Overflow**: Aggregated value must not exceed uint128 max

## Error Handling

The package defines specific error types for validation failures:

- `ErrNoReceipts`: No receipts provided for aggregation
- `ErrAggregateOverflow`: Aggregated value exceeds uint128 max
- `ErrDuplicateSignature`: Duplicate receipt signature detected
- `ErrInvalidTimestamp`: Receipt timestamp not greater than previous RAV
- `ErrCollectionMismatch`: Receipts have different collection IDs
- `ErrPayerMismatch`: Receipts have different payer addresses
- `ErrServiceProviderMismatch`: Receipts have different service provider addresses
- `ErrDataServiceMismatch`: Receipts have different data service addresses
- `ErrInvalidSigner`: Receipt signed by unauthorized signer
- `ErrRAVSignerMismatch`: Previous RAV signed by unauthorized signer

## EIP-712 Domain

The package uses the following EIP-712 domain configuration for V2 (Horizon):

- **Name**: `"GraphTallyCollector"`
- **Version**: `"1"`
- **Chain ID**: Configurable (e.g., 1 for mainnet, 42161 for Arbitrum One)
- **Verifying Contract**: GraphTallyCollector contract address

## Security Features

### Signature Malleability Protection

The package implements signature normalization to prevent malleability attacks. All signatures are converted to low-S canonical form, ensuring that malleated signatures are detected as duplicates.

### Timestamp Validation

Receipts must have timestamps greater than any previous RAV to prevent replay attacks.

### Overflow Protection

The aggregator checks that the sum of receipt values does not exceed uint128 maximum value.

## Testing

Run the unit tests:

```bash
go test ./...
```

Run tests with verbose output:

```bash
go test -v ./...
```

## Dependencies

- `github.com/streamingfast/eth-go` - Ethereum types and crypto operations
- `github.com/stretchr/testify` - Testing assertions (test only)

## License

See the main repository for license information.

## References

- [EIP-712: Typed structured data hashing and signing](https://eips.ethereum.org/EIPS/eip-712)
- [The Graph Protocol](https://thegraph.com/)
- [Timeline Aggregation Protocol (TAP)](https://github.com/semiotic-ai/timeline-aggregation-protocol)
- [Graph Protocol Contracts (Horizon)](https://github.com/graphprotocol/contracts/tree/main/packages/horizon)
