# Integration Test Contracts

This document describes the smart contracts used in horizon-go integration tests and their relationship to the source graphprotocol-contracts.

## Overview

The integration tests use a combination of reimplemented and mock contracts to test the Go implementation of TAP (Timeline Aggregation Protocol) without requiring the full protocol stack.

## Contracts Tested 1:1 with Source Project

| Contract | Status |
|----------|--------|
| **None** | No contracts are used directly from the source graphprotocol-contracts repo |

All contracts are either reimplemented with identical critical logic or mocked for testing purposes.

## Reimplemented Contracts (Faithful to Source Logic)

### GraphTallyCollectorFull

**File:** `test/integration/build/contracts/GraphTallyCollectorFull.sol`

The core TAP collector contract reimplemented with **identical EIP-712 logic** to the source.

**Critical Implementation Details:**

- **EIP-712 Typehash** (must match source exactly):
  ```solidity
  keccak256("ReceiptAggregateVoucher(bytes32 collectionId,address payer,address serviceProvider,address dataService,uint64 timestampNs,uint128 valueAggregate,bytes metadata)")
  ```

- **EIP-712 Domain:** `name="GraphTallyCollector"`, `version="1"`

- **RAV Structure** (identical field order and types):
  ```solidity
  struct ReceiptAggregateVoucher {
      bytes32 collectionId;
      address payer;
      address serviceProvider;
      address dataService;
      uint64 timestampNs;
      uint128 valueAggregate;
      bytes metadata;
  }
  ```

- **tokensCollected Mapping:**
  ```solidity
  mapping(address dataService => mapping(bytes32 collectionId => mapping(address receiver => mapping(address payer => uint256 tokens)))) public tokensCollected;
  ```

- **Signature Recovery:** Uses OpenZeppelin's `ECDSA.recover` (same as source)

### Authorizable

**File:** `test/integration/build/contracts/GraphTallyCollectorFull.sol` (embedded)

Signer authorization contract with thawing period support.

**Functions:**
- `authorizeSigner(address signer)` - Authorize a signer for the caller
- `revokeSigner(address signer)` - Revoke authorization (after thawing)
- `thawSigner(address signer)` - Start thawing period
- `cancelThaw(address signer)` - Cancel pending thaw
- `isAuthorized(address authorizer, address signer)` - Check authorization

**Note:** In tests, `revokeSignerThawingPeriod` is set to 0 for immediate revocation.

## Mock Contracts (Simplified for Testing)

**File:** `test/integration/build/contracts/IntegrationTestContracts.sol`

### MockGRTToken

Standard ERC20 token with public minting capability.

| Function | Description |
|----------|-------------|
| `mint(address to, uint256 amount)` | Public mint function for testing |

**Simplification:** No governance or access control on minting.

### MockController

Protocol registry that maps contract IDs to addresses.

| Function | Description |
|----------|-------------|
| `setContractProxy(bytes32 id, address contractAddress)` | Register a contract |
| `getContractProxy(bytes32 id)` | Look up a contract |
| `paused()` / `partialPaused()` | Always returns false |

**Simplification:** No governance, just a simple `bytes32 => address` mapping.

### MockStaking

Simplified staking contract for provision verification.

| Function | Description |
|----------|-------------|
| `setProvision(address sp, address ds, uint256 tokens)` | Set provision amount |
| `getProviderTokensAvailable(address sp, address ds)` | Get provision amount |

**Simplification:** No actual staking, slashing, or delegation logic.

### MockPaymentsEscrow

Payment escrow with deposit and collection functionality.

| Function | Description |
|----------|-------------|
| `deposit(address sender, uint256 amount)` | Deposit GRT to escrow |
| `getEscrowAmount(address sender, address receiver)` | Query escrow balance |
| `collect(...)` | Collect payment with PPM-based cuts |

**Simplifications:**
- Uses `sender => sender` mapping instead of receiver-specific escrow
- Direct token transfers instead of complex payment routing
- PPM-based data service cut calculation (1,000,000 = 100%)

### GraphDirectoryMock

**File:** `test/integration/build/contracts/GraphTallyCollectorFull.sol` (embedded)

Abstract contract that looks up mock contracts from the controller.

**Registry Keys:**
- `keccak256("GraphToken")` - GRT token
- `keccak256("HorizonStaking")` - Staking contract
- `keccak256("PaymentsEscrow")` - Escrow contract

## Verification-Only Contract

### GraphTallyVerifier

**File:** `test/integration/build/contracts/GraphTallyVerifier.sol`

Minimal contract for testing EIP-712 signature verification without protocol dependencies.

| Function | Description |
|----------|-------------|
| `domainSeparator()` | Returns EIP-712 domain separator |
| `encodeRAV(rav)` | Computes EIP-712 typed data hash |
| `recoverRAVSigner(signedRAV)` | Recovers signer from signature |
| `structHash(rav)` | Computes struct hash (without domain) |

**Used for testing:**
- Domain separator compatibility between Go and Solidity
- RAV hash encoding compatibility
- Signature recovery compatibility

## Critical Compatibility Points

| Aspect | Implementation | Test Coverage |
|--------|----------------|---------------|
| EIP-712 Domain Separator | Chain ID + Verifying Contract | `TestDomainSeparatorCompatibility` |
| RAV Struct Hash | Typehash + ABI-encoded fields | `TestEIP712HashCompatibility` |
| Signature Format | R+S+V (65 bytes) | `TestSignatureRecoveryCompatibility` |
| Signer Authorization | Payer authorizes signer | `TestAuthorizeSignerFlow` |
| Incremental Collection | tokensCollected delta tracking | `TestCollectRAVIncremental` |

## What's NOT Tested

The following production features are not covered by integration tests:

- Full protocol governance
- Staking/slashing mechanics
- Complex escrow receiver mappings
- Thawing period enforcement (set to 0 in tests)
- Multi-hop payment routing via GraphPayments
- Protocol pause functionality

## Signature Format Note

The Go implementation uses `eth.Signature` which stores signatures as V+R+S (65 bytes), but Solidity's `ECDSA.recover` expects R+S+V format. The `encodeCollectData` function in `collect_test.go` handles this conversion:

```go
// eth.Signature is V+R+S (65 bytes) but Solidity ECDSA.recover expects R+S+V
copy(paddedSig[0:32], sig[1:33])   // R (32 bytes)
copy(paddedSig[32:64], sig[33:65]) // S (32 bytes)
paddedSig[64] = sig[0]             // V (1 byte)
```
