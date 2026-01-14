# Implementation Plan: Golang RAV (Receipt Aggregated Voucher) - V2 Only

## Overview

This document outlines the implementation plan for porting Receipt Aggregated Voucher (RAV) functionality from Rust to Golang. The RAV system is part of The Graph's Timeline Aggregation Protocol (TAP), which enables efficient micropayments by aggregating individual payment receipts into a single voucher that can be redeemed on-chain.

**Scope:** This implementation supports **only V2 (Horizon)** mode with the `GraphTallyCollector` contract. V1 (legacy TAP) is explicitly out of scope.

## Implementation Status

**STATUS: ✅ CORE IMPLEMENTATION COMPLETE**

All core phases (1-4) are implemented and fully tested:
- ✅ Core types with JSON serialization
- ✅ EIP-712 signing and hashing
- ✅ Signature handling with malleability protection
- ✅ Receipt aggregation with comprehensive validation
- ✅ 29 unit tests passing
- ✅ Working example demonstrating usage
- ✅ Complete documentation

Integration tests with testcontainers deferred to future iteration.

## Current Rust Implementation Analysis

### Key Components Analyzed

1. **tap_eip712_message** - EIP-712 signing and verification
2. **tap_graph/v2** - V2 Receipt and RAV data structures
3. **tap_core** - Core protocol logic and domain separator
4. **tap_aggregator/v2** - V2 Aggregation service

### V2 Data Structures (Horizon - Collection-based)

#### V2 Receipt
```solidity
struct Receipt {
    bytes32 collection_id;      // Collection identifier (derived from allocation)
    address payer;              // Payer address
    address data_service;       // Data service address
    address service_provider;   // Service provider address
    uint64 timestamp_ns;        // Unix timestamp in nanoseconds
    uint64 nonce;               // Random collision avoidance
    uint128 value;              // GRT value
}
```

#### V2 RAV (ReceiptAggregateVoucher)
```solidity
struct ReceiptAggregateVoucher {
    bytes32 collectionId;       // Collection ID
    address payer;              // Payer address
    address serviceProvider;    // Service provider address
    address dataService;        // Data service address
    uint64 timestampNs;         // Max timestamp from aggregated receipts
    uint128 valueAggregate;     // Total aggregated value
    bytes metadata;             // Extensible metadata
}
```

### EIP-712 Domain Configuration (V2)

- **Name:** `"GraphTallyCollector"`
- **Version:** `"1"`
- **Chain ID:** (configurable, e.g., 1 for mainnet, 42161 for Arbitrum One)
- **Verifying Contract:** GraphTallyCollector contract address

### EIP-712 Type Hashes

**V2 Receipt Type Hash:**
```
keccak256("Receipt(bytes32 collection_id,address payer,address data_service,address service_provider,uint64 timestamp_ns,uint64 nonce,uint128 value)")
```

**V2 RAV Type Hash:**
```
keccak256("ReceiptAggregateVoucher(bytes32 collectionId,address payer,address serviceProvider,address dataService,uint64 timestampNs,uint128 valueAggregate,bytes metadata)")
```

---

## Dependencies Strategy

### Primary: streamingfast/eth-go

Use `github.com/streamingfast/eth-go` for:
- `eth.Address` - 20-byte Ethereum address
- `eth.Hash` - 32-byte hash (for collectionId, message hashes)
- `eth.PrivateKey` - ECDSA private key wrapper with `Sign(messageHash)` method
- `eth.Signature` - 65-byte signature with `Recover(messageHash)` method
- `eth.Keccak256()` - Keccak-256 hashing (used internally)

### Standard Go Crypto

Use standard library where possible:
- `crypto/ecdsa` - For any additional ECDSA operations if needed
- `math/big` - For `uint128` value handling
- `encoding/binary` - For byte encoding
- `encoding/json` - For serialization

### Minimal External Dependencies

- `github.com/streamingfast/eth-go` - Ethereum types and crypto
- `github.com/testcontainers/testcontainers-go` - Integration testing only

---

## Golang Implementation Plan

### Project Structure

```
golang-rav/
├── go.mod
├── go.sum
├── rav/
│   ├── types.go              # Receipt and RAV structs
│   ├── types_test.go
│   ├── signed_message.go     # Generic signed message wrapper
│   ├── signed_message_test.go
│   ├── eip712.go             # EIP-712 domain and encoding
│   ├── eip712_test.go
│   ├── aggregator.go         # RAV aggregation logic
│   ├── aggregator_test.go
│   ├── validation.go         # Receipt validation checks
│   └── validation_test.go
├── internal/
│   └── keccak/
│       └── keccak.go         # Keccak256 wrapper (uses eth-go)
├── test/
│   └── integration/
│       ├── main_test.go      # TestMain with contract build logic
│       ├── setup_test.go     # Test environment setup (Geth + deploy)
│       ├── rav_test.go       # Integration tests
│       ├── build/
│       │   ├── Dockerfile    # Contract build container
│       │   └── build.sh      # Build script
│       └── testdata/
│           └── contracts/    # Committed to git
│               └── GraphTallyCollector.json
└── examples/
    └── basic/
        └── main.go           # Usage example
```

### Phase 1: Core Types

#### 1.1 Receipt and RAV Types (rav/types.go)

```go
package rav

import (
    "encoding/json"
    "math/big"
    "time"

    "github.com/streamingfast/eth-go"
)

// CollectionID is a 32-byte identifier for a collection (derived from allocation)
type CollectionID [32]byte

// MarshalJSON implements json.Marshaler
func (c CollectionID) MarshalJSON() ([]byte, error) {
    return json.Marshal(eth.Hash(c[:]).Pretty())
}

// UnmarshalJSON implements json.Unmarshaler
func (c *CollectionID) UnmarshalJSON(data []byte) error {
    var s string
    if err := json.Unmarshal(data, &s); err != nil {
        return err
    }
    h := eth.MustNewHash(s)
    copy(c[:], h)
    return nil
}

// Receipt represents a V2 TAP receipt (Horizon - collection-based)
type Receipt struct {
    CollectionID    CollectionID `json:"collection_id"`
    Payer           eth.Address  `json:"payer"`
    DataService     eth.Address  `json:"data_service"`
    ServiceProvider eth.Address  `json:"service_provider"`
    TimestampNs     uint64       `json:"timestamp_ns"`
    Nonce           uint64       `json:"nonce"`
    Value           *big.Int     `json:"value"`
}

// NewReceipt creates a new receipt with current timestamp and random nonce
func NewReceipt(
    collectionID CollectionID,
    payer, dataService, serviceProvider eth.Address,
    value *big.Int,
) *Receipt {
    return &Receipt{
        CollectionID:    collectionID,
        Payer:           payer,
        DataService:     dataService,
        ServiceProvider: serviceProvider,
        TimestampNs:     uint64(time.Now().UnixNano()),
        Nonce:           randomUint64(),
        Value:           new(big.Int).Set(value),
    }
}

// RAV represents a V2 Receipt Aggregate Voucher (Horizon)
type RAV struct {
    CollectionID    CollectionID `json:"collectionId"`
    Payer           eth.Address  `json:"payer"`
    ServiceProvider eth.Address  `json:"serviceProvider"`
    DataService     eth.Address  `json:"dataService"`
    TimestampNs     uint64       `json:"timestampNs"`
    ValueAggregate  *big.Int     `json:"valueAggregate"`
    Metadata        []byte       `json:"metadata"`
}

// MaxUint128 is the maximum value for uint128
var MaxUint128 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
```

#### 1.2 Signed Message Wrapper (rav/signed_message.go)

```go
package rav

import (
    "fmt"

    "github.com/streamingfast/eth-go"
)

// SignedMessage wraps a message with its EIP-712 signature
type SignedMessage[T any] struct {
    Message   T             `json:"message"`
    Signature eth.Signature `json:"signature"`
}

// SignedReceipt is a receipt with its signature
type SignedReceipt = SignedMessage[*Receipt]

// SignedRAV is a RAV with its signature
type SignedRAV = SignedMessage[*RAV]

// Sign creates a signed message using the domain and private key
func Sign[T EIP712Encodable](domain *Domain, message T, key *eth.PrivateKey) (*SignedMessage[T], error) {
    messageHash, err := HashTypedData(domain, message)
    if err != nil {
        return nil, fmt.Errorf("computing typed data hash: %w", err)
    }

    sig, err := key.Sign(messageHash)
    if err != nil {
        return nil, fmt.Errorf("signing message: %w", err)
    }

    return &SignedMessage[T]{
        Message:   message,
        Signature: sig,
    }, nil
}

// RecoverSigner recovers the signer address from the signature
func (sm *SignedMessage[T]) RecoverSigner(domain *Domain) (eth.Address, error) {
    // Type assertion to get the EIP712Encodable interface
    msg, ok := any(sm.Message).(EIP712Encodable)
    if !ok {
        return eth.Address{}, fmt.Errorf("message does not implement EIP712Encodable")
    }

    messageHash, err := HashTypedData(domain, msg)
    if err != nil {
        return eth.Address{}, fmt.Errorf("computing typed data hash: %w", err)
    }

    return sm.Signature.Recover(messageHash)
}

// UniqueID returns the signature bytes for uniqueness checking
// Uses normalized (low-S) form to prevent malleability attacks
func (sm *SignedMessage[T]) UniqueID() [65]byte {
    return normalizeSignature(sm.Signature)
}
```

### Phase 2: EIP-712 Implementation

#### 2.1 Domain and Encoding (rav/eip712.go)

```go
package rav

import (
    "encoding/binary"
    "fmt"
    "math/big"

    "github.com/streamingfast/eth-go"
)

// EIP712Encodable is implemented by types that can be EIP-712 encoded
type EIP712Encodable interface {
    EIP712TypeHash() eth.Hash
    EIP712EncodeData() []byte
}

// Domain represents an EIP-712 domain separator for V2 (Horizon)
type Domain struct {
    Name              string
    Version           string
    ChainID           *big.Int
    VerifyingContract eth.Address
}

// EIP712 type hashes (pre-computed)
var (
    eip712DomainTypeHash = keccak256([]byte(
        "EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))

    receiptTypeHash = keccak256([]byte(
        "Receipt(bytes32 collection_id,address payer,address data_service,address service_provider,uint64 timestamp_ns,uint64 nonce,uint128 value)"))

    ravTypeHash = keccak256([]byte(
        "ReceiptAggregateVoucher(bytes32 collectionId,address payer,address serviceProvider,address dataService,uint64 timestampNs,uint128 valueAggregate,bytes metadata)"))
)

// NewDomain creates a V2 Horizon EIP-712 domain
func NewDomain(chainID uint64, verifyingContract eth.Address) *Domain {
    return &Domain{
        Name:              "GraphTallyCollector",
        Version:           "1",
        ChainID:           big.NewInt(int64(chainID)),
        VerifyingContract: verifyingContract,
    }
}

// Separator computes the EIP-712 domain separator hash
func (d *Domain) Separator() eth.Hash {
    encoded := make([]byte, 0, 32*5)
    encoded = append(encoded, eip712DomainTypeHash[:]...)
    encoded = append(encoded, keccak256([]byte(d.Name))[:]...)
    encoded = append(encoded, keccak256([]byte(d.Version))[:]...)
    encoded = append(encoded, padLeft(d.ChainID.Bytes(), 32)...)
    encoded = append(encoded, padLeft(d.VerifyingContract[:], 32)...)

    return keccak256(encoded)
}

// EIP712TypeHash returns the type hash for Receipt
func (r *Receipt) EIP712TypeHash() eth.Hash {
    return receiptTypeHash
}

// EIP712EncodeData returns the ABI-encoded data for Receipt
func (r *Receipt) EIP712EncodeData() []byte {
    encoded := make([]byte, 0, 32*7)
    encoded = append(encoded, r.CollectionID[:]...)                    // bytes32
    encoded = append(encoded, padLeft(r.Payer[:], 32)...)              // address
    encoded = append(encoded, padLeft(r.DataService[:], 32)...)        // address
    encoded = append(encoded, padLeft(r.ServiceProvider[:], 32)...)    // address
    encoded = append(encoded, encodeUint64(r.TimestampNs)...)          // uint64
    encoded = append(encoded, encodeUint64(r.Nonce)...)                // uint64
    encoded = append(encoded, encodeUint128(r.Value)...)               // uint128
    return encoded
}

// EIP712TypeHash returns the type hash for RAV
func (r *RAV) EIP712TypeHash() eth.Hash {
    return ravTypeHash
}

// EIP712EncodeData returns the ABI-encoded data for RAV
func (r *RAV) EIP712EncodeData() []byte {
    encoded := make([]byte, 0, 32*7)
    encoded = append(encoded, r.CollectionID[:]...)                    // bytes32
    encoded = append(encoded, padLeft(r.Payer[:], 32)...)              // address
    encoded = append(encoded, padLeft(r.ServiceProvider[:], 32)...)    // address
    encoded = append(encoded, padLeft(r.DataService[:], 32)...)        // address
    encoded = append(encoded, encodeUint64(r.TimestampNs)...)          // uint64
    encoded = append(encoded, encodeUint128(r.ValueAggregate)...)      // uint128
    encoded = append(encoded, keccak256(r.Metadata)[:]...)             // keccak256(bytes)
    return encoded
}

// HashTypedData computes the EIP-712 hash for signing
// Returns: keccak256("\x19\x01" || domainSeparator || structHash)
func HashTypedData[T EIP712Encodable](domain *Domain, message T) (eth.Hash, error) {
    structHash := hashStruct(message)
    domainSep := domain.Separator()

    // EIP-712: "\x19\x01" || domainSeparator || structHash
    data := make([]byte, 0, 2+32+32)
    data = append(data, 0x19, 0x01)
    data = append(data, domainSep[:]...)
    data = append(data, structHash[:]...)

    return keccak256(data), nil
}

// hashStruct computes keccak256(typeHash || encodeData)
func hashStruct[T EIP712Encodable](message T) eth.Hash {
    typeHash := message.EIP712TypeHash()
    encodedData := message.EIP712EncodeData()

    data := make([]byte, 0, 32+len(encodedData))
    data = append(data, typeHash[:]...)
    data = append(data, encodedData...)

    return keccak256(data)
}

// Helper functions

func keccak256(data []byte) eth.Hash {
    return eth.Keccak256(data)
}

func padLeft(b []byte, size int) []byte {
    if len(b) >= size {
        return b[len(b)-size:]
    }
    result := make([]byte, size)
    copy(result[size-len(b):], b)
    return result
}

func encodeUint64(v uint64) []byte {
    result := make([]byte, 32)
    binary.BigEndian.PutUint64(result[24:], v)
    return result
}

func encodeUint128(v *big.Int) []byte {
    result := make([]byte, 32)
    if v != nil {
        b := v.Bytes()
        copy(result[32-len(b):], b)
    }
    return result
}
```

### Phase 3: Signature Handling

#### 3.1 Signature Normalization (rav/signature.go)

```go
package rav

import (
    "math/big"

    "github.com/streamingfast/eth-go"
)

// secp256k1 curve order N
var secp256k1N, _ = new(big.Int).SetString(
    "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)
var secp256k1HalfN = new(big.Int).Rsh(secp256k1N, 1)

// normalizeSignature returns signature in low-S canonical form
// This prevents signature malleability attacks where the same message
// can have two valid signatures that recover to the same address
func normalizeSignature(sig eth.Signature) [65]byte {
    var result [65]byte
    copy(result[:], sig[:])

    // Extract S value (bytes 32-64)
    s := new(big.Int).SetBytes(sig[32:64])

    // If S > N/2, replace with N - S and flip V
    if s.Cmp(secp256k1HalfN) > 0 {
        s = new(big.Int).Sub(secp256k1N, s)
        sBytes := s.Bytes()
        // Zero out and copy normalized S
        for i := 32; i < 64; i++ {
            result[i] = 0
        }
        copy(result[64-len(sBytes):64], sBytes)
        // Flip V (recovery bit)
        result[64] ^= 1
    }

    return result
}

// SignaturesEqual compares two signatures in normalized form
func SignaturesEqual(a, b eth.Signature) bool {
    normA := normalizeSignature(a)
    normB := normalizeSignature(b)
    return normA == normB
}
```

### Phase 4: RAV Aggregation Logic

#### 4.1 Aggregation (rav/aggregator.go)

```go
package rav

import (
    "errors"
    "math/big"

    "github.com/streamingfast/eth-go"
)

var (
    ErrNoReceipts             = errors.New("no valid receipts for RAV request")
    ErrAggregateOverflow      = errors.New("aggregating receipt results in overflow")
    ErrDuplicateSignature     = errors.New("duplicate receipt signature detected")
    ErrInvalidTimestamp       = errors.New("receipt timestamp not greater than previous RAV")
    ErrCollectionMismatch     = errors.New("receipts have different collection IDs")
    ErrPayerMismatch          = errors.New("receipts have different payer addresses")
    ErrServiceProviderMismatch = errors.New("receipts have different service provider addresses")
    ErrDataServiceMismatch    = errors.New("receipts have different data service addresses")
    ErrInvalidSigner          = errors.New("receipt signed by unauthorized signer")
    ErrRAVSignerMismatch      = errors.New("previous RAV signed by unauthorized signer")
)

// Aggregator handles receipt validation and RAV generation
type Aggregator struct {
    domain          *Domain
    signerKey       *eth.PrivateKey
    acceptedSigners map[eth.Address]bool
}

// NewAggregator creates a new RAV aggregator
func NewAggregator(domain *Domain, signerKey *eth.PrivateKey, acceptedSigners []eth.Address) *Aggregator {
    signerMap := make(map[eth.Address]bool, len(acceptedSigners))
    for _, addr := range acceptedSigners {
        signerMap[addr] = true
    }

    return &Aggregator{
        domain:          domain,
        signerKey:       signerKey,
        acceptedSigners: signerMap,
    }
}

// AggregateReceipts validates receipts and creates a signed RAV
func (a *Aggregator) AggregateReceipts(
    receipts []*SignedReceipt,
    previousRAV *SignedRAV,
) (*SignedRAV, error) {

    if len(receipts) == 0 {
        return nil, ErrNoReceipts
    }

    // Validate signatures are unique (malleability protection)
    if err := a.checkSignaturesUnique(receipts); err != nil {
        return nil, err
    }

    // Verify all receipts are from accepted signers
    if err := a.verifyReceiptSigners(receipts); err != nil {
        return nil, err
    }

    // Verify previous RAV signer if present
    if previousRAV != nil {
        if err := a.verifyRAVSigner(previousRAV); err != nil {
            return nil, err
        }
    }

    // Check receipt timestamps are after previous RAV
    if err := checkReceiptTimestamps(receipts, previousRAV); err != nil {
        return nil, err
    }

    // Validate field consistency across all receipts
    if err := validateReceiptConsistency(receipts); err != nil {
        return nil, err
    }

    // Verify previous RAV fields match receipts
    if previousRAV != nil {
        if err := validateRAVConsistency(receipts[0].Message, previousRAV.Message); err != nil {
            return nil, err
        }
    }

    // Perform aggregation
    rav, err := aggregate(receipts, previousRAV)
    if err != nil {
        return nil, err
    }

    // Sign and return
    return Sign(a.domain, rav, a.signerKey)
}

// aggregate creates a RAV from validated receipts
func aggregate(receipts []*SignedReceipt, previousRAV *SignedRAV) (*RAV, error) {
    first := receipts[0].Message

    var timestampMax uint64 = 0
    valueAggregate := big.NewInt(0)

    // Initialize from previous RAV if present
    if previousRAV != nil {
        timestampMax = previousRAV.Message.TimestampNs
        valueAggregate = new(big.Int).Set(previousRAV.Message.ValueAggregate)
    }

    // Aggregate all receipts
    for _, r := range receipts {
        receipt := r.Message

        // Add value with overflow check
        newValue := new(big.Int).Add(valueAggregate, receipt.Value)
        if newValue.Cmp(MaxUint128) > 0 {
            return nil, ErrAggregateOverflow
        }
        valueAggregate = newValue

        // Track max timestamp
        if receipt.TimestampNs > timestampMax {
            timestampMax = receipt.TimestampNs
        }
    }

    return &RAV{
        CollectionID:    first.CollectionID,
        Payer:           first.Payer,
        ServiceProvider: first.ServiceProvider,
        DataService:     first.DataService,
        TimestampNs:     timestampMax,
        ValueAggregate:  valueAggregate,
        Metadata:        []byte{}, // Empty metadata by default
    }, nil
}

func (a *Aggregator) checkSignaturesUnique(receipts []*SignedReceipt) error {
    seen := make(map[[65]byte]bool, len(receipts))
    for _, r := range receipts {
        normalized := normalizeSignature(r.Signature)
        if seen[normalized] {
            return ErrDuplicateSignature
        }
        seen[normalized] = true
    }
    return nil
}

func (a *Aggregator) verifyReceiptSigners(receipts []*SignedReceipt) error {
    for _, r := range receipts {
        signer, err := r.RecoverSigner(a.domain)
        if err != nil {
            return err
        }
        if !a.acceptedSigners[signer] {
            return ErrInvalidSigner
        }
    }
    return nil
}

func (a *Aggregator) verifyRAVSigner(rav *SignedRAV) error {
    signer, err := rav.RecoverSigner(a.domain)
    if err != nil {
        return err
    }
    if !a.acceptedSigners[signer] {
        return ErrRAVSignerMismatch
    }
    return nil
}

func checkReceiptTimestamps(receipts []*SignedReceipt, previousRAV *SignedRAV) error {
    if previousRAV == nil {
        return nil
    }
    ravTimestamp := previousRAV.Message.TimestampNs
    for _, r := range receipts {
        if r.Message.TimestampNs <= ravTimestamp {
            return ErrInvalidTimestamp
        }
    }
    return nil
}

func validateReceiptConsistency(receipts []*SignedReceipt) error {
    if len(receipts) == 0 {
        return nil
    }

    first := receipts[0].Message
    for _, r := range receipts[1:] {
        if r.Message.CollectionID != first.CollectionID {
            return ErrCollectionMismatch
        }
        if r.Message.Payer != first.Payer {
            return ErrPayerMismatch
        }
        if r.Message.ServiceProvider != first.ServiceProvider {
            return ErrServiceProviderMismatch
        }
        if r.Message.DataService != first.DataService {
            return ErrDataServiceMismatch
        }
    }
    return nil
}

func validateRAVConsistency(receipt *Receipt, rav *RAV) error {
    if receipt.CollectionID != rav.CollectionID {
        return ErrCollectionMismatch
    }
    if receipt.Payer != rav.Payer {
        return ErrPayerMismatch
    }
    if receipt.ServiceProvider != rav.ServiceProvider {
        return ErrServiceProviderMismatch
    }
    if receipt.DataService != rav.DataService {
        return ErrDataServiceMismatch
    }
    return nil
}
```

### Phase 5: Integration Tests with Testcontainers

#### Developer Experience

New developers simply need Docker running and can execute:
```bash
# Run all tests including integration tests
go test ./...

# Run only integration tests
go test ./test/integration/...

# Force rebuild of contract artifacts (normally cached in testdata/)
FORCE_CONTRACTS_BUILD=true go test ./test/integration/...
```

No Docker Compose, no manual setup. Contract artifacts are committed to git for fast test runs.

#### Architecture

The integration test setup is split into two phases:

1. **Contract Build Phase (TestMain)**: Checks if compiled contract artifacts exist in `testdata/contracts/`. If missing or `FORCE_CONTRACTS_BUILD=true`, runs a Docker container to build contracts and copy artifacts to the local filesystem. These artifacts are committed to git.

2. **Contract Deploy Phase (per-test setup)**: Deploys contracts to Geth using eth-go's standard transaction API. Each test run gets fresh contract deployments with known addresses.

#### 5.1 Project Structure

```
test/
└── integration/
    ├── main_test.go          # TestMain with contract build logic
    ├── setup_test.go         # Test environment setup (Geth + deploy)
    ├── rav_test.go           # Integration tests
    ├── build/
    │   ├── Dockerfile        # Contract build container
    │   └── build.sh          # Build script
    └── testdata/
        └── contracts/        # Committed to git
            └── GraphTallyCollector.json  # ABI + bytecode
```

#### 5.2 Contract Artifacts

Contract artifacts are JSON files containing ABI and bytecode, committed to git.

**test/integration/testdata/contracts/GraphTallyCollector.json:**
```json
{
    "contractName": "GraphTallyCollector",
    "abi": [...],
    "bytecode": "0x..."
}
```

#### 5.3 Contract Build Container

This container only runs when artifacts are missing or force rebuild is requested.

**test/integration/build/Dockerfile:**
```dockerfile
FROM node:20-alpine

WORKDIR /app

# Install dependencies
RUN apk add --no-cache git curl bash jq

# Install foundry
RUN curl -L https://foundry.paradigm.xyz | bash && \
    /root/.foundry/bin/foundryup

ENV PATH="/root/.foundry/bin:${PATH}"

# Clone Graph Protocol contracts (Horizon)
RUN git clone --depth 1 https://github.com/graphprotocol/contracts.git /app/contracts

WORKDIR /app/contracts

# Install npm dependencies and build
RUN npm install
RUN forge build

# Copy build script
COPY build.sh /app/build.sh
RUN chmod +x /app/build.sh

# Output directory will be mounted
VOLUME /output

ENTRYPOINT ["/app/build.sh"]
```

**test/integration/build/build.sh:**
```bash
#!/bin/bash
set -e

echo "Building contract artifacts..."

cd /app/contracts/packages/horizon

# Build contracts
forge build

# Extract artifact for GraphTallyCollector
ARTIFACT_PATH="out/GraphTallyCollector.sol/GraphTallyCollector.json"

if [ ! -f "$ARTIFACT_PATH" ]; then
    echo "ERROR: Contract artifact not found at $ARTIFACT_PATH"
    exit 1
fi

# Copy to output directory (mounted volume)
echo "Copying artifacts to /output..."
cp "$ARTIFACT_PATH" /output/GraphTallyCollector.json

echo "Build complete!"
ls -la /output/
```

#### 5.4 Integration Test Setup

**test/integration/main_test.go:**
```go
package integration

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "runtime"
    "time"

    "github.com/testcontainers/testcontainers-go"
    "github.com/testcontainers/testcontainers-go/wait"
)

// getTestDir returns the absolute path to the integration test directory
func getTestDir() string {
    _, currentFile, _, ok := runtime.Caller(0)
    if !ok {
        panic("failed to get current file path")
    }
    return filepath.Dir(currentFile)
}

func TestMain(m *testing.M) {
    if err := ensureContractArtifacts(); err != nil {
        fmt.Fprintf(os.Stderr, "Failed to ensure contract artifacts: %v\n", err)
        os.Exit(1)
    }

    os.Exit(m.Run())
}

// ensureContractArtifacts checks if contract artifacts exist, builds them if needed
func ensureContractArtifacts() error {
    testDir := getTestDir()
    artifactsDir := filepath.Join(testDir, "testdata", "contracts")
    collectorArtifact := filepath.Join(artifactsDir, "GraphTallyCollector.json")

    // Check if artifacts already exist
    forceBuild := os.Getenv("FORCE_CONTRACTS_BUILD") == "true"
    if !forceBuild {
        if _, err := os.Stat(collectorArtifact); err == nil {
            fmt.Println("Contract artifacts found, skipping build")
            return nil
        }
    }

    fmt.Println("Building contract artifacts...")

    // Ensure output directory exists
    if err := os.MkdirAll(artifactsDir, 0755); err != nil {
        return fmt.Errorf("creating artifacts directory: %w", err)
    }

    // Run build container
    ctx := context.Background()
    buildDir := filepath.Join(testDir, "build")

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
            WithStartupTimeout(300 * time.Second),
    }

    container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
        ContainerRequest: req,
        Started:          true,
    })
    if err != nil {
        return fmt.Errorf("starting build container: %w", err)
    }
    defer container.Terminate(ctx)

    // Wait for container to complete
    state, err := container.State(ctx)
    if err != nil {
        return fmt.Errorf("getting container state: %w", err)
    }

    if state.ExitCode != 0 {
        logs, _ := container.Logs(ctx)
        if logs != nil {
            defer logs.Close()
            // Read and print logs for debugging
        }
        return fmt.Errorf("build container exited with code %d", state.ExitCode)
    }

    // Verify artifact was created
    if _, err := os.Stat(collectorArtifact); err != nil {
        return fmt.Errorf("artifact not found after build: %w", err)
    }

    fmt.Println("Contract artifacts built successfully")
    return nil
}
```

**test/integration/setup_test.go:**
```go
package integration

import (
    "context"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "math/big"
    "os"
    "path/filepath"
    "sync"
    "testing"
    "time"

    "github.com/streamingfast/eth-go"
    "github.com/testcontainers/testcontainers-go"
    "github.com/testcontainers/testcontainers-go/wait"
)

// ContractArtifact represents a compiled contract
type ContractArtifact struct {
    ContractName string          `json:"contractName"`
    ABI          json.RawMessage `json:"abi"`
    Bytecode     string          `json:"bytecode"`
}

// TestEnv holds the test environment state
type TestEnv struct {
    ctx              context.Context
    gethContainer    testcontainers.Container
    RPCURL           string
    ChainID          uint64
    CollectorAddress eth.Address
    DeployerKey      *eth.PrivateKey
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
    if sharedEnvErr != nil {
        t.Fatalf("Failed to setup test environment: %v", sharedEnvErr)
    }
    return sharedEnv
}

func setupEnv() (*TestEnv, error) {
    ctx := context.Background()

    // Start Geth container in dev mode
    gethReq := testcontainers.ContainerRequest{
        Image: "ethereum/client-go:stable",
        Cmd: []string{
            "--dev",
            "--dev.period=1",
            "--http",
            "--http.addr=0.0.0.0",
            "--http.port=8545",
            "--http.api=eth,net,web3,personal,debug",
            "--http.corsdomain=*",
        },
        ExposedPorts: []string{"8545/tcp"},
        WaitingFor: wait.ForHTTP("/").
            WithPort("8545/tcp").
            WithMethod("POST").
            WithBody(`{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}`).
            WithHeaders(map[string]string{"Content-Type": "application/json"}).
            WithResponseMatcher(func(body io.Reader) bool {
                var resp map[string]interface{}
                return json.NewDecoder(body).Decode(&resp) == nil
            }).
            WithStartupTimeout(60 * time.Second),
    }

    gethContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
        ContainerRequest: gethReq,
        Started:          true,
    })
    if err != nil {
        return nil, fmt.Errorf("starting geth container: %w", err)
    }

    mappedPort, err := gethContainer.MappedPort(ctx, "8545/tcp")
    if err != nil {
        gethContainer.Terminate(ctx)
        return nil, fmt.Errorf("getting mapped port: %w", err)
    }

    host, err := gethContainer.Host(ctx)
    if err != nil {
        gethContainer.Terminate(ctx)
        return nil, fmt.Errorf("getting host: %w", err)
    }

    rpcURL := fmt.Sprintf("http://%s:%s", host, mappedPort.Port())

    // Create RPC client
    client := eth.NewClient(rpcURL)

    // Get chain ID
    chainID, err := client.ChainID(ctx)
    if err != nil {
        gethContainer.Terminate(ctx)
        return nil, fmt.Errorf("getting chain ID: %w", err)
    }

    // Get dev account (funded by Geth --dev)
    accounts, err := client.Accounts(ctx)
    if err != nil || len(accounts) == 0 {
        gethContainer.Terminate(ctx)
        return nil, fmt.Errorf("getting dev accounts: %w", err)
    }
    devAccount := accounts[0]

    // For Geth --dev, we need to use personal_unlockAccount or sign differently
    // Actually, we'll create our own key and fund it from dev account
    deployerKey, err := eth.NewRandomPrivateKey()
    if err != nil {
        gethContainer.Terminate(ctx)
        return nil, fmt.Errorf("generating deployer key: %w", err)
    }
    deployerAddr := deployerKey.PublicKey().Address()

    // Fund deployer from dev account (using personal API since dev account is unlocked)
    fundAmount := big.NewInt(1e18) // 1 ETH
    if err := fundFromDevAccount(ctx, client, devAccount, deployerAddr, fundAmount); err != nil {
        gethContainer.Terminate(ctx)
        return nil, fmt.Errorf("funding deployer: %w", err)
    }

    // Load contract artifact
    artifact, err := loadContractArtifact("GraphTallyCollector")
    if err != nil {
        gethContainer.Terminate(ctx)
        return nil, fmt.Errorf("loading contract artifact: %w", err)
    }

    // Deploy contract
    collectorAddr, err := deployContract(ctx, client, deployerKey, chainID.Uint64(), artifact)
    if err != nil {
        gethContainer.Terminate(ctx)
        return nil, fmt.Errorf("deploying contract: %w", err)
    }

    return &TestEnv{
        ctx:              ctx,
        gethContainer:    gethContainer,
        RPCURL:           rpcURL,
        ChainID:          chainID.Uint64(),
        CollectorAddress: collectorAddr,
        DeployerKey:      deployerKey,
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

func fundFromDevAccount(ctx context.Context, client *eth.Client, from, to eth.Address, amount *big.Int) error {
    // Use eth_sendTransaction with unlocked dev account
    tx := map[string]interface{}{
        "from":  from.Pretty(),
        "to":    to.Pretty(),
        "value": fmt.Sprintf("0x%x", amount),
    }

    var txHash string
    if err := client.Call(ctx, &txHash, "eth_sendTransaction", tx); err != nil {
        return fmt.Errorf("sending fund transaction: %w", err)
    }

    // Wait for transaction receipt
    return waitForReceipt(ctx, client, txHash)
}

func deployContract(ctx context.Context, client *eth.Client, key *eth.PrivateKey, chainID uint64, artifact *ContractArtifact) (eth.Address, error) {
    // Decode bytecode
    bytecode, err := hex.DecodeString(artifact.Bytecode[2:]) // Remove 0x prefix
    if err != nil {
        return eth.Address{}, fmt.Errorf("decoding bytecode: %w", err)
    }

    deployerAddr := key.PublicKey().Address()

    // Get nonce
    nonce, err := client.PendingNonceAt(ctx, deployerAddr)
    if err != nil {
        return eth.Address{}, fmt.Errorf("getting nonce: %w", err)
    }

    // Get gas price
    gasPrice, err := client.SuggestGasPrice(ctx)
    if err != nil {
        return eth.Address{}, fmt.Errorf("getting gas price: %w", err)
    }

    // Estimate gas (use a high estimate for contract deployment)
    gasLimit := uint64(3000000)

    // Create deployment transaction
    tx := eth.NewTransaction(
        nonce,
        eth.Address{}, // Empty address for contract creation
        big.NewInt(0),
        gasLimit,
        gasPrice,
        bytecode,
    )

    // Sign transaction
    signedTx, err := eth.SignTx(tx, eth.NewEIP155Signer(big.NewInt(int64(chainID))), key)
    if err != nil {
        return eth.Address{}, fmt.Errorf("signing transaction: %w", err)
    }

    // Send transaction
    if err := client.SendTransaction(ctx, signedTx); err != nil {
        return eth.Address{}, fmt.Errorf("sending transaction: %w", err)
    }

    txHash := signedTx.Hash().Hex()

    // Wait for receipt
    if err := waitForReceipt(ctx, client, txHash); err != nil {
        return eth.Address{}, fmt.Errorf("waiting for receipt: %w", err)
    }

    // Get receipt to find contract address
    receipt, err := client.TransactionReceipt(ctx, eth.MustNewHash(txHash))
    if err != nil {
        return eth.Address{}, fmt.Errorf("getting receipt: %w", err)
    }

    if receipt.ContractAddress == (eth.Address{}) {
        return eth.Address{}, fmt.Errorf("contract address not in receipt")
    }

    return receipt.ContractAddress, nil
}

func waitForReceipt(ctx context.Context, client *eth.Client, txHash string) error {
    timeout := time.After(30 * time.Second)
    ticker := time.NewTicker(500 * time.Millisecond)
    defer ticker.Stop()

    for {
        select {
        case <-timeout:
            return fmt.Errorf("timeout waiting for transaction %s", txHash)
        case <-ticker.C:
            receipt, err := client.TransactionReceipt(ctx, eth.MustNewHash(txHash))
            if err != nil {
                continue // Not mined yet
            }
            if receipt.Status == 0 {
                return fmt.Errorf("transaction failed: %s", txHash)
            }
            return nil
        case <-ctx.Done():
            return ctx.Err()
        }
    }
}

// Cleanup terminates the test environment
func (env *TestEnv) Cleanup() {
    if env.gethContainer != nil {
        env.gethContainer.Terminate(env.ctx)
    }
}
```

#### 5.5 Integration Tests

**test/integration/rav_test.go:**
```go
package integration

import (
    "math/big"
    "testing"
    "time"

    "github.com/streamingfast/eth-go"
    "github.com/stretchr/testify/require"

    "github.com/yourorg/golang-rav/rav"
)

func TestReceiptSigningAndRecovery(t *testing.T) {
    env := SetupEnv(t)

    // Generate test wallet
    key, err := eth.NewRandomPrivateKey()
    require.NoError(t, err)

    expectedSigner := key.PublicKey().Address()

    // Create domain using deployed contract address
    domain := rav.NewDomain(env.ChainID, env.CollectorAddress)

    // Create receipt
    var collectionID rav.CollectionID
    copy(collectionID[:], eth.MustNewHash("0xabababababababababababababababababababababababababababababababab")[:])

    receipt := rav.NewReceipt(
        collectionID,
        expectedSigner,
        eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
        eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
        big.NewInt(1000),
    )

    // Sign
    signedReceipt, err := rav.Sign(domain, receipt, key)
    require.NoError(t, err)

    // Recover and verify
    recoveredSigner, err := signedReceipt.RecoverSigner(domain)
    require.NoError(t, err)
    require.Equal(t, expectedSigner, recoveredSigner)
}

func TestRAVAggregation(t *testing.T) {
    env := SetupEnv(t)

    // Generate keys
    senderKey, _ := eth.NewRandomPrivateKey()
    aggregatorKey, _ := eth.NewRandomPrivateKey()

    senderAddr := senderKey.PublicKey().Address()

    domain := rav.NewDomain(env.ChainID, env.CollectorAddress)

    // Create collection ID
    var collectionID rav.CollectionID
    copy(collectionID[:], eth.MustNewHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")[:])

    payer := senderAddr
    dataService := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
    serviceProvider := eth.MustNewAddress("0x2222222222222222222222222222222222222222")

    // Create multiple receipts
    var receipts []*rav.SignedReceipt
    totalValue := big.NewInt(0)

    for i := 0; i < 10; i++ {
        value := big.NewInt(int64(100 + i*10))

        receipt := &rav.Receipt{
            CollectionID:    collectionID,
            Payer:           payer,
            DataService:     dataService,
            ServiceProvider: serviceProvider,
            TimestampNs:     uint64(time.Now().UnixNano()) + uint64(i),
            Nonce:           uint64(i),
            Value:           value,
        }

        signed, err := rav.Sign(domain, receipt, senderKey)
        require.NoError(t, err)

        receipts = append(receipts, signed)
        totalValue.Add(totalValue, value)
    }

    // Create aggregator
    aggregator := rav.NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr})

    // Aggregate
    signedRAV, err := aggregator.AggregateReceipts(receipts, nil)
    require.NoError(t, err)

    // Verify RAV properties
    require.Equal(t, collectionID, signedRAV.Message.CollectionID)
    require.Equal(t, payer, signedRAV.Message.Payer)
    require.Equal(t, serviceProvider, signedRAV.Message.ServiceProvider)
    require.Equal(t, dataService, signedRAV.Message.DataService)
    require.Equal(t, 0, signedRAV.Message.ValueAggregate.Cmp(totalValue))
}

func TestSignatureMalleabilityProtection(t *testing.T) {
    env := SetupEnv(t)

    key, _ := eth.NewRandomPrivateKey()
    domain := rav.NewDomain(env.ChainID, env.CollectorAddress)

    var collectionID rav.CollectionID
    receipt := &rav.Receipt{
        CollectionID:    collectionID,
        Payer:           key.PublicKey().Address(),
        DataService:     eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
        ServiceProvider: eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
        TimestampNs:     uint64(time.Now().UnixNano()),
        Nonce:           12345,
        Value:           big.NewInt(1000),
    }

    signed, _ := rav.Sign(domain, receipt, key)

    // Create malleated signature (high-S form)
    malleatedSig := createMalleatedSignature(signed.Signature)
    malleatedReceipt := &rav.SignedReceipt{
        Message:   signed.Message,
        Signature: malleatedSig,
    }

    // Both should recover to same signer
    signer1, _ := signed.RecoverSigner(domain)
    signer2, _ := malleatedReceipt.RecoverSigner(domain)
    require.Equal(t, signer1, signer2)

    // But aggregator should detect as duplicate
    aggregatorKey, _ := eth.NewRandomPrivateKey()
    aggregator := rav.NewAggregator(domain, aggregatorKey, []eth.Address{key.PublicKey().Address()})

    receipts := []*rav.SignedReceipt{signed, malleatedReceipt}
    _, err := aggregator.AggregateReceipts(receipts, nil)
    require.ErrorIs(t, err, rav.ErrDuplicateSignature)
}

func TestIncrementalRAVAggregation(t *testing.T) {
    env := SetupEnv(t)

    senderKey, _ := eth.NewRandomPrivateKey()
    aggregatorKey, _ := eth.NewRandomPrivateKey()

    senderAddr := senderKey.PublicKey().Address()
    domain := rav.NewDomain(env.ChainID, env.CollectorAddress)
    aggregator := rav.NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr, aggregatorKey.PublicKey().Address()})

    var collectionID rav.CollectionID
    payer := senderAddr
    dataService := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
    serviceProvider := eth.MustNewAddress("0x2222222222222222222222222222222222222222")

    // First batch of receipts
    var batch1 []*rav.SignedReceipt
    baseTimestamp := uint64(time.Now().UnixNano())

    for i := 0; i < 5; i++ {
        receipt := &rav.Receipt{
            CollectionID:    collectionID,
            Payer:           payer,
            DataService:     dataService,
            ServiceProvider: serviceProvider,
            TimestampNs:     baseTimestamp + uint64(i),
            Nonce:           uint64(i),
            Value:           big.NewInt(100),
        }
        signed, _ := rav.Sign(domain, receipt, senderKey)
        batch1 = append(batch1, signed)
    }

    // First RAV
    rav1, err := aggregator.AggregateReceipts(batch1, nil)
    require.NoError(t, err)
    require.Equal(t, big.NewInt(500), rav1.Message.ValueAggregate)

    // Second batch (timestamps must be > first RAV's timestamp)
    var batch2 []*rav.SignedReceipt
    for i := 0; i < 5; i++ {
        receipt := &rav.Receipt{
            CollectionID:    collectionID,
            Payer:           payer,
            DataService:     dataService,
            ServiceProvider: serviceProvider,
            TimestampNs:     rav1.Message.TimestampNs + uint64(i) + 1,
            Nonce:           uint64(100 + i),
            Value:           big.NewInt(200),
        }
        signed, _ := rav.Sign(domain, receipt, senderKey)
        batch2 = append(batch2, signed)
    }

    // Second RAV (incremental)
    rav2, err := aggregator.AggregateReceipts(batch2, rav1)
    require.NoError(t, err)
    require.Equal(t, big.NewInt(1500), rav2.Message.ValueAggregate) // 500 + 1000
}

func TestReceiptTimestampValidation(t *testing.T) {
    env := SetupEnv(t)

    senderKey, _ := eth.NewRandomPrivateKey()
    aggregatorKey, _ := eth.NewRandomPrivateKey()

    senderAddr := senderKey.PublicKey().Address()
    domain := rav.NewDomain(env.ChainID, env.CollectorAddress)
    aggregator := rav.NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr, aggregatorKey.PublicKey().Address()})

    var collectionID rav.CollectionID
    payer := senderAddr
    dataService := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
    serviceProvider := eth.MustNewAddress("0x2222222222222222222222222222222222222222")

    baseTimestamp := uint64(time.Now().UnixNano())

    // Create initial receipts and RAV
    var initialReceipts []*rav.SignedReceipt
    for i := 0; i < 3; i++ {
        receipt := &rav.Receipt{
            CollectionID:    collectionID,
            Payer:           payer,
            DataService:     dataService,
            ServiceProvider: serviceProvider,
            TimestampNs:     baseTimestamp + uint64(i),
            Nonce:           uint64(i),
            Value:           big.NewInt(100),
        }
        signed, _ := rav.Sign(domain, receipt, senderKey)
        initialReceipts = append(initialReceipts, signed)
    }

    rav1, err := aggregator.AggregateReceipts(initialReceipts, nil)
    require.NoError(t, err)

    // Try to aggregate receipt with timestamp <= previous RAV timestamp
    oldReceipt := &rav.Receipt{
        CollectionID:    collectionID,
        Payer:           payer,
        DataService:     dataService,
        ServiceProvider: serviceProvider,
        TimestampNs:     rav1.Message.TimestampNs, // Same timestamp - should fail
        Nonce:           uint64(999),
        Value:           big.NewInt(100),
    }
    oldSigned, _ := rav.Sign(domain, oldReceipt, senderKey)

    _, err = aggregator.AggregateReceipts([]*rav.SignedReceipt{oldSigned}, rav1)
    require.ErrorIs(t, err, rav.ErrInvalidTimestamp)
}

func TestUnauthorizedSigner(t *testing.T) {
    env := SetupEnv(t)

    authorizedKey, _ := eth.NewRandomPrivateKey()
    unauthorizedKey, _ := eth.NewRandomPrivateKey()
    aggregatorKey, _ := eth.NewRandomPrivateKey()

    domain := rav.NewDomain(env.ChainID, env.CollectorAddress)

    // Only authorize one key
    aggregator := rav.NewAggregator(domain, aggregatorKey, []eth.Address{authorizedKey.PublicKey().Address()})

    var collectionID rav.CollectionID
    receipt := &rav.Receipt{
        CollectionID:    collectionID,
        Payer:           unauthorizedKey.PublicKey().Address(),
        DataService:     eth.MustNewAddress("0x1111111111111111111111111111111111111111"),
        ServiceProvider: eth.MustNewAddress("0x2222222222222222222222222222222222222222"),
        TimestampNs:     uint64(time.Now().UnixNano()),
        Nonce:           1,
        Value:           big.NewInt(100),
    }

    // Sign with unauthorized key
    signed, _ := rav.Sign(domain, receipt, unauthorizedKey)

    _, err := aggregator.AggregateReceipts([]*rav.SignedReceipt{signed}, nil)
    require.ErrorIs(t, err, rav.ErrInvalidSigner)
}

func TestCollectionIDMismatch(t *testing.T) {
    env := SetupEnv(t)

    senderKey, _ := eth.NewRandomPrivateKey()
    aggregatorKey, _ := eth.NewRandomPrivateKey()

    senderAddr := senderKey.PublicKey().Address()
    domain := rav.NewDomain(env.ChainID, env.CollectorAddress)
    aggregator := rav.NewAggregator(domain, aggregatorKey, []eth.Address{senderAddr})

    payer := senderAddr
    dataService := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
    serviceProvider := eth.MustNewAddress("0x2222222222222222222222222222222222222222")

    var collectionID1 rav.CollectionID
    copy(collectionID1[:], eth.MustNewHash("0x1111111111111111111111111111111111111111111111111111111111111111")[:])

    var collectionID2 rav.CollectionID
    copy(collectionID2[:], eth.MustNewHash("0x2222222222222222222222222222222222222222222222222222222222222222")[:])

    receipt1 := &rav.Receipt{
        CollectionID:    collectionID1,
        Payer:           payer,
        DataService:     dataService,
        ServiceProvider: serviceProvider,
        TimestampNs:     uint64(time.Now().UnixNano()),
        Nonce:           1,
        Value:           big.NewInt(100),
    }

    receipt2 := &rav.Receipt{
        CollectionID:    collectionID2, // Different collection ID
        Payer:           payer,
        DataService:     dataService,
        ServiceProvider: serviceProvider,
        TimestampNs:     uint64(time.Now().UnixNano()) + 1,
        Nonce:           2,
        Value:           big.NewInt(100),
    }

    signed1, _ := rav.Sign(domain, receipt1, senderKey)
    signed2, _ := rav.Sign(domain, receipt2, senderKey)

    _, err := aggregator.AggregateReceipts([]*rav.SignedReceipt{signed1, signed2}, nil)
    require.ErrorIs(t, err, rav.ErrCollectionMismatch)
}

// Helper to create malleated (high-S) signature
func createMalleatedSignature(sig eth.Signature) eth.Signature {
    var result eth.Signature
    copy(result[:], sig[:])

    // secp256k1 curve order
    n, _ := new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)

    s := new(big.Int).SetBytes(sig[32:64])
    sNew := new(big.Int).Sub(n, s)

    sBytes := sNew.Bytes()
    for i := 32; i < 64; i++ {
        result[i] = 0
    }
    copy(result[64-len(sBytes):64], sBytes)
    result[64] ^= 1 // Flip V

    return result
}
```

---

## Dependencies

### go.mod

```go
module github.com/yourorg/golang-rav

go 1.21

require (
    github.com/streamingfast/eth-go v0.0.0-latest
    github.com/testcontainers/testcontainers-go v0.27.0
    github.com/stretchr/testify v1.8.4
)
```

### Contract Dependencies

From `https://github.com/graphprotocol/contracts`:
- `packages/horizon/contracts/payments/collectors/GraphTallyCollector.sol`

---

## Implementation Checklist

### Phase 1: Core Types ✅ COMPLETED
- [x] Define CollectionID type (32 bytes)
- [x] Define V2 Receipt struct
- [x] Define V2 RAV struct
- [x] Implement JSON serialization
- [x] Implement SignedMessage generic wrapper
- [x] Add helper constructors
- [x] Unit tests for types

### Phase 2: EIP-712 ✅ COMPLETED
- [x] Implement Domain struct (V2 only: "GraphTallyCollector")
- [x] Implement domain separator computation
- [x] Implement Receipt struct hash encoding
- [x] Implement RAV struct hash encoding
- [x] Implement HashTypedData function
- [x] Unit tests comparing against Rust implementation

### Phase 3: Signature Handling ✅ COMPLETED
- [x] Implement signature normalization (low-S form)
- [x] Implement Sign function using eth-go PrivateKey
- [x] Implement RecoverSigner function using eth-go Signature
- [x] Implement UniqueID for malleability protection
- [x] Unit tests for signature operations

### Phase 4: Aggregation ✅ COMPLETED
- [x] Implement Aggregator struct
- [x] Implement signature uniqueness check
- [x] Implement signer verification
- [x] Implement timestamp validation
- [x] Implement field consistency validation
- [x] Implement aggregate function
- [x] Unit tests for all validation cases

### Phase 5: Integration Tests (Testcontainers) ⏭️ DEFERRED
Integration tests with testcontainers have been deferred to a future iteration.
The core functionality is fully tested with comprehensive unit tests.

Future work:
- [ ] Create contract build Dockerfile (test/integration/build/)
- [ ] Create build script that outputs artifacts to mounted volume
- [ ] Implement TestMain with contract artifact check
- [ ] Implement test environment setup with Geth container
- [ ] Deploy contracts via eth-go signed transactions
- [ ] Commit initial contract artifacts to git
- [ ] End-to-end tests with deployed contracts

### Phase 6: Documentation & Examples ✅ COMPLETED
- [x] Package documentation (README.md)
- [x] Usage examples (examples/basic/main.go)
- [x] API reference in README

---

## Key Differences from Rust Implementation

| Aspect | Rust | Go |
|--------|------|-----|
| Versions | V1 + V2 | **V2 only** |
| Ethereum lib | alloy/ethers | streamingfast/eth-go |
| Generics | Full trait system | Go 1.18+ generics |
| Error handling | Result<T, E> | (T, error) |
| Big integers | U128/U256 | math/big.Int |

---

## References

- [EIP-712: Typed structured data hashing and signing](https://eips.ethereum.org/EIPS/eip-712)
- [TAP Protocol Repository](https://github.com/semiotic-ai/timeline-aggregation-protocol)
- [Graph Protocol Contracts (Horizon)](https://github.com/graphprotocol/contracts/tree/main/packages/horizon)
- [streamingfast/eth-go](https://github.com/streamingfast/eth-go)
- [testcontainers-go](https://github.com/testcontainers/testcontainers-go)
