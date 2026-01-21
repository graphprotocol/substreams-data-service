# Implementation Plan: Migrate Integration Tests to Original GraphTallyCollector

## ULTIMATE GOAL

Replace the mock `GraphTallyCollectorFull.sol` with the original `GraphTallyCollector` from `horizon-contracts/packages/horizon/contracts/payments/collectors/GraphTallyCollector.sol`. This will ensure integration tests run against production-identical contract logic for signer authorization and RAV collection.

## Status: COMPLETED

**Last Updated**: 2026-01-19

**Completion Date**: 2026-01-19

### Summary of Changes
- Added `GraphTallyCollector` import to `OriginalContracts.sol`
- Updated `build.sh` to extract original GraphTallyCollector artifact
- Created `authorization_helpers_test.go` with `GenerateSignerProof()` function
- Updated `setup_test.go` to deploy and load original GraphTallyCollector
- Updated `authorization_test.go` with proof-based `callAuthorizeSigner()` and two-step revoke flow
- Updated `collect_test.go` with explicit signer authorization (original doesn't support self-authorization)
- Skipped `TestDomainSeparatorCompatibility` (original doesn't expose `domainSeparator()` function)
- Deleted `GraphTallyCollectorFull.sol` and `GraphTallyVerifier.sol` (no longer needed)
- All 16 integration tests pass (15 pass + 1 skip)

---

## Executive Summary

| Aspect | Details |
|--------|---------|
| **Objective** | Replace mock GraphTallyCollectorFull with original GraphTallyCollector |
| **Primary Change** | Implement authorization proof generation in Go |
| **Complexity** | Medium (cryptographic proof implementation + test updates) |
| **Critical Difference** | Original requires signer proof: `authorizeSigner(signer, proofDeadline, proof)` |
| **Files Affected** | ~7 files (modify: 5, delete: 1, create: 1) |

---

## Background: Current vs Original Implementation

### Current Mock Implementation (GraphTallyCollectorFull.sol)

The current mock at `test/integration/build/contracts/GraphTallyCollectorFull.sol` uses a **simplified Authorizable**:

```solidity
// Simplified - NO proof required
function authorizeSigner(address signer) external {
    require(!_authorized[msg.sender][signer], AuthorizableSignerAlreadyAuthorized(signer));
    _authorized[msg.sender][signer] = true;
    delete _thawEndTimestamp[msg.sender][signer];
    emit SignerAuthorized(msg.sender, signer);
}

// Uses simple 2-level mapping: authorizer -> signer -> bool
mapping(address authorizer => mapping(address authorized => bool)) private _authorized;

// isAuthorized returns true for self OR if explicitly authorized
function _isAuthorized(address authorizer, address signer) internal view returns (bool) {
    return authorizer == signer || _authorized[authorizer][signer];
}

// Simple revoke - checks thawing but 0 thawing period allows instant revoke
function revokeSigner(address signer) external {
    require(_authorized[msg.sender][signer], AuthorizableSignerNotAuthorized(signer));
    require(_thawEndTimestamp[msg.sender][signer] <= block.timestamp, ...);
    delete _authorized[msg.sender][signer];
}
```

### Original Implementation (Authorizable.sol)

The original `horizon-contracts/packages/horizon/contracts/utilities/Authorizable.sol` uses a **proof-based authorization**:

```solidity
// Original - REQUIRES cryptographic proof from signer
function authorizeSigner(address signer, uint256 proofDeadline, bytes calldata proof) external {
    require(
        authorizations[signer].authorizer == address(0),
        AuthorizableSignerAlreadyAuthorized(...)
    );
    _verifyAuthorizationProof(proof, proofDeadline, signer);
    authorizations[signer].authorizer = msg.sender;
    emit SignerAuthorized(msg.sender, signer);
}

// Uses struct with authorizer tracking - signer can only be used by ONE authorizer
struct Authorization {
    address authorizer;      // The authorizer who authorized this signer
    uint256 thawEndTimestamp; // When thawing ends
    bool revoked;            // Whether authorization has been revoked
}
mapping(address signer => Authorization authorization) public authorizations;

// isAuthorized checks authorizer matches AND not revoked
// NOTE: Self-authorization is NOT supported in original
function _isAuthorized(address _authorizer, address _signer) internal view returns (bool) {
    return (_authorizer != address(0) &&
        authorizations[_signer].authorizer == _authorizer &&
        !authorizations[_signer].revoked);
}

// Revoke requires prior thawSigner() call
function revokeAuthorizedSigner(address signer) external onlyAuthorized(signer) {
    uint256 thawEndTimestamp = authorizations[signer].thawEndTimestamp;
    require(thawEndTimestamp > 0, AuthorizableSignerNotThawing(signer));
    require(thawEndTimestamp <= block.timestamp, AuthorizableSignerStillThawing(...));
    authorizations[signer].revoked = true;
}
```

### Key Differences Summary

| Feature | Mock | Original |
|---------|------|----------|
| **Authorization Function** | `authorizeSigner(signer)` | `authorizeSigner(signer, proofDeadline, proof)` |
| **Proof Required** | No | Yes - signer must sign consent message |
| **Self-Authorization** | Yes (`authorizer == signer`) | No - must be explicitly authorized |
| **Signer Reuse** | Yes - same signer for multiple authorizers | No - signer bound to single authorizer |
| **Storage Model** | `mapping(authorizer => mapping(signer => bool))` | `mapping(signer => Authorization struct)` |
| **Revoke Function** | `revokeSigner(signer)` | `revokeAuthorizedSigner(signer)` |
| **Revoke Process** | Instant if thawing=0 | Requires `thawSigner()` first |
| **GraphDirectory** | Custom `GraphDirectoryMock` | Real `GraphDirectory` (reads "Staking" key) |

---

## Implementation Tasks

### Phase 1: Build System Updates

- [ ] **1.1 Update OriginalContracts.sol** - Add GraphTallyCollector import
  - File: `test/integration/build/contracts/OriginalContracts.sol`
  - Add: `import {GraphTallyCollector} from "@graphprotocol/horizon/contracts/payments/collectors/GraphTallyCollector.sol";`

- [ ] **1.2 Update build.sh** - Add GraphTallyCollector to extraction list
  - File: `test/integration/build/build.sh`
  - Add "GraphTallyCollector" to contracts array
  - Add search path for `out/GraphTallyCollector.sol/GraphTallyCollector.json`
  - Remove "GraphTallyCollectorFull" from contracts array

- [ ] **1.3 Delete mock GraphTallyCollectorFull.sol**
  - File: `test/integration/build/contracts/GraphTallyCollectorFull.sol`
  - This file will be deleted after verifying original works

### Phase 2: Authorization Proof Implementation in Go

- [ ] **2.1 Create authorization proof helper** - New function to generate signer proofs
  - File: `test/integration/authorization_helpers_test.go` (new file)
  - Implement `GenerateSignerProof()` function

**Proof Generation Algorithm:**

```go
// GenerateSignerProof generates a proof for authorizing a signer
// The proof is the signer's signature over a message containing:
// - chainId (uint256)
// - collectorAddress (address - this contract)
// - "authorizeSignerProof" (string literal)
// - proofDeadline (uint256)
// - authorizer (address - msg.sender who will call authorizeSigner)
func GenerateSignerProof(
    chainID uint64,
    collectorAddress eth.Address,
    proofDeadline uint64,
    authorizer eth.Address,
    signerKey *eth.PrivateKey,
) ([]byte, error) {
    // Build message: abi.encodePacked(chainid, address(this), "authorizeSignerProof", deadline, msg.sender)
    message := make([]byte, 0, 124) // 32 + 20 + 20 + 32 + 20 = 124 bytes

    // chainId as uint256 (32 bytes, big-endian)
    chainIDBytes := make([]byte, 32)
    new(big.Int).SetUint64(chainID).FillBytes(chainIDBytes)
    message = append(message, chainIDBytes...)

    // collectorAddress (20 bytes, NOT left-padded - encodePacked)
    message = append(message, collectorAddress[:]...)

    // "authorizeSignerProof" (20 bytes string literal)
    message = append(message, []byte("authorizeSignerProof")...)

    // proofDeadline as uint256 (32 bytes, big-endian)
    deadlineBytes := make([]byte, 32)
    new(big.Int).SetUint64(proofDeadline).FillBytes(deadlineBytes)
    message = append(message, deadlineBytes...)

    // authorizer address (20 bytes, NOT left-padded - encodePacked)
    message = append(message, authorizer[:]...)

    // Hash the message
    messageHash := eth.Keccak256(message)

    // Create Ethereum signed message hash: keccak256("\x19Ethereum Signed Message:\n32" + hash)
    prefix := []byte("\x19Ethereum Signed Message:\n32")
    digest := eth.Keccak256(append(prefix, messageHash...))

    // Sign with signer's key
    sig, err := signerKey.Sign(digest)
    if err != nil {
        return nil, fmt.Errorf("signing proof: %w", err)
    }

    // Convert eth.Signature (V+R+S) to Solidity format (R+S+V)
    proof := make([]byte, 65)
    copy(proof[0:32], sig[1:33])  // R
    copy(proof[32:64], sig[33:65]) // S
    proof[64] = sig[0]             // V

    return proof, nil
}
```

- [ ] **2.2 Create callAuthorizeSignerWithProof helper**
  - File: `test/integration/authorization_test.go`
  - Update or replace `callAuthorizeSigner()` to use proof mechanism

```go
// callAuthorizeSignerWithProof calls Authorizable.authorizeSigner(address signer, uint256 proofDeadline, bytes proof)
func callAuthorizeSignerWithProof(ctx testContext, rpcURL string, authorizerKey *eth.PrivateKey, chainID uint64, collector eth.Address, signerKey *eth.PrivateKey, abi *eth.ABI) error {
    authorizerAddr := authorizerKey.PublicKey().Address()
    signerAddr := signerKey.PublicKey().Address()

    // Generate proof with deadline 1 hour in the future
    proofDeadline := uint64(time.Now().Add(1 * time.Hour).Unix())

    proof, err := GenerateSignerProof(chainID, collector, proofDeadline, authorizerAddr, signerKey)
    if err != nil {
        return fmt.Errorf("generating signer proof: %w", err)
    }

    // Find authorizeSigner function with 3 parameters
    authorizeSignerFn := abi.FindFunctionByName("authorizeSigner")
    if authorizeSignerFn == nil {
        return fmt.Errorf("authorizeSigner function not found in ABI")
    }

    // Encode call: authorizeSigner(address signer, uint256 proofDeadline, bytes proof)
    data, err := authorizeSignerFn.NewCall(signerAddr, new(big.Int).SetUint64(proofDeadline), proof).Encode()
    if err != nil {
        return fmt.Errorf("encoding authorizeSigner call: %w", err)
    }

    return sendTransaction(ctx, rpcURL, authorizerKey, chainID, &collector, big.NewInt(0), data)
}
```

### Phase 3: Test Setup Updates

- [ ] **3.1 Update setup_test.go deployment**
  - File: `test/integration/setup_test.go`
  - Change artifact name from "GraphTallyCollectorFull" to "GraphTallyCollector"
  - Update log message to indicate original contract

- [ ] **3.2 Update ABI loading**
  - File: `test/integration/setup_test.go`
  - Change `loadABI("GraphTallyCollectorFull")` to `loadABI("GraphTallyCollector")`

- [ ] **3.3 Remove self-authorization assumption in collect tests**
  - File: `test/integration/collect_test.go`
  - Tests currently assume payer can sign their own RAVs (self-authorization)
  - With original contract, must explicitly authorize payer as signer OR use separate signer

### Phase 4: Test Updates

- [ ] **4.1 Update TestCollectRAV**
  - File: `test/integration/collect_test.go`
  - Add explicit authorization: payer authorizes themselves OR use separate signer
  - Option A: Payer authorizes self by generating proof
  - Option B: Use separate signer key (matches production pattern better)

- [ ] **4.2 Update TestCollectRAVIncremental**
  - File: `test/integration/collect_test.go`
  - Same authorization update as TestCollectRAV

- [ ] **4.3 Update TestAuthorizeSignerFlow**
  - File: `test/integration/authorization_test.go`
  - Replace `callAuthorizeSigner()` with `callAuthorizeSignerWithProof()`
  - Requires passing signerKey (not just address) to generate proof

- [ ] **4.4 Update TestUnauthorizedSignerFails**
  - File: `test/integration/authorization_test.go`
  - Should still work - unauthorized signer means no proof was generated

- [ ] **4.5 Update TestRevokeSignerFlow**
  - File: `test/integration/authorization_test.go`
  - Replace `callRevokeSigner()` with two-step process:
    1. `callThawSigner()` - start thawing
    2. Wait for thawing period (0 in tests)
    3. `callRevokeAuthorizedSigner()` - complete revocation

### Phase 5: Revoke Function Updates

- [ ] **5.1 Create callThawSigner helper**
  - File: `test/integration/authorization_test.go`
  - Implement `callThawSigner(ctx, rpcURL, key, chainID, collector, signer, abi)`

- [ ] **5.2 Create callRevokeAuthorizedSigner helper**
  - File: `test/integration/authorization_test.go`
  - Implement `callRevokeAuthorizedSigner(ctx, rpcURL, key, chainID, collector, signer, abi)`

- [ ] **5.3 Update callRevokeSigner to use new flow**
  - Either replace with two calls OR create wrapper that does both
  - Since thawing period is 0, can call thaw then revoke immediately

### Phase 6: Build and Verify

- [ ] **6.1 Rebuild contract artifacts**
  - Run Docker build to generate new artifacts
  - Verify GraphTallyCollector.json is created

- [ ] **6.2 Run all integration tests**
  - Ensure all 16 tests pass with original contract
  - Verify EIP-712 signatures still work correctly

- [ ] **6.3 Delete GraphTallyCollectorFull.sol**
  - Only after all tests pass
  - Clean up testdata directory

---

## Files to Change

| File | Change Type | Description |
|------|-------------|-------------|
| `test/integration/build/contracts/OriginalContracts.sol` | Modify | Add GraphTallyCollector import |
| `test/integration/build/build.sh` | Modify | Update contract list |
| `test/integration/setup_test.go` | Modify | Change artifact names |
| `test/integration/authorization_helpers_test.go` | **New** | Proof generation function |
| `test/integration/authorization_test.go` | Modify | Update authorization functions |
| `test/integration/collect_test.go` | Modify | Add explicit signer authorization |
| `test/integration/build/contracts/GraphTallyCollectorFull.sol` | **Delete** | No longer needed |

---

## Implementation Details: Authorization Proof

### Solidity Verification Logic (from Authorizable.sol)

```solidity
function _verifyAuthorizationProof(bytes calldata _proof, uint256 _proofDeadline, address _signer) private view {
    // Check that the proofDeadline has not passed
    require(
        _proofDeadline > block.timestamp,
        AuthorizableInvalidSignerProofDeadline(_proofDeadline, block.timestamp)
    );

    // Generate the message hash
    bytes32 messageHash = keccak256(
        abi.encodePacked(block.chainid, address(this), "authorizeSignerProof", _proofDeadline, msg.sender)
    );

    // Generate the allegedly signed digest
    bytes32 digest = MessageHashUtils.toEthSignedMessageHash(messageHash);

    // Verify that the recovered signer matches the to be authorized signer
    require(ECDSA.recover(digest, _proof) == _signer, AuthorizableInvalidSignerProof());
}
```

### Message Structure (abi.encodePacked)

| Field | Type | Bytes | Notes |
|-------|------|-------|-------|
| chainId | uint256 | 32 | `block.chainid` |
| contractAddress | address | 20 | `address(this)` - collector contract |
| literal | string | 20 | `"authorizeSignerProof"` |
| proofDeadline | uint256 | 32 | Unix timestamp |
| authorizer | address | 20 | `msg.sender` - who calls authorizeSigner |
| **Total** | | **124** | |

### Ethereum Signed Message Format

```
digest = keccak256("\x19Ethereum Signed Message:\n32" + keccak256(message))
```

---

## Test Strategy Updates

### Current Pattern (Self-Authorization Works)

```go
// Payer signs RAV, payer is automatically authorized as signer for themselves
signedRAV, _ := horizon.Sign(domain, rav, env.PayerKey)
callCollect(..., signedRAV, ...) // Works because authorizer == signer
```

### New Pattern (Explicit Authorization Required)

**Option A: Payer authorizes themselves (requires proof)**

```go
// Payer must first authorize themselves as a signer
proof, _ := GenerateSignerProof(chainID, collector, deadline, payerAddr, payerKey)
callAuthorizeSignerWithProof(..., payerKey, chainID, collector, payerKey, ...)

// Now payer can sign RAVs
signedRAV, _ := horizon.Sign(domain, rav, env.PayerKey)
callCollect(..., signedRAV, ...) // Works because payer authorized themselves
```

**Option B: Separate signer key (matches production)**

```go
// Create a dedicated signer key
signerKey, _ := eth.NewRandomPrivateKey()

// Payer authorizes the signer
proof, _ := GenerateSignerProof(chainID, collector, deadline, payerAddr, signerKey)
callAuthorizeSignerWithProof(..., payerKey, chainID, collector, signerKey, ...)

// Signer signs RAVs on behalf of payer
signedRAV, _ := horizon.Sign(domain, rav, signerKey)
callCollect(..., signedRAV, ...) // Works because signer is authorized by payer
```

**Recommendation**: Use Option A for simplicity in collect tests, keeping current RAV signing flow mostly unchanged. The authorization tests already demonstrate the separate signer pattern.

---

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Proof generation bugs | Test proof against Solidity verification in unit test |
| Signature format mismatch | Verify R+S+V ordering matches ECDSA.recover expectations |
| encodePacked vs encode confusion | Use exact byte layout matching Solidity |
| Self-authorization removal breaks tests | Add explicit authorization step to collect tests |
| Revoke flow change breaks tests | Update to two-step thaw+revoke process |

---

## Success Criteria

1. All 16 integration tests pass using original GraphTallyCollector
2. Authorization proof generation produces valid signatures
3. Self-authorization tests updated to use explicit authorization
4. Revoke tests updated to use thaw+revoke flow
5. GraphTallyCollectorFull.sol deleted from codebase
6. Build system produces GraphTallyCollector.json artifact

---

## Dependencies

- Existing original contracts already deployed: PaymentsEscrow, GraphPayments
- MockStaking already implements required IHorizonStaking methods
- MockController already registers "Staking" key (used by GraphDirectory)

---

## Appendix A: Contract Registry Keys

The original GraphTallyCollector uses `GraphDirectory` which reads from Controller:

| Key Name | Contract | Notes |
|----------|----------|-------|
| `"GraphToken"` | MockGRTToken | ERC20 token |
| `"Staking"` | MockStaking | Note: NOT "HorizonStaking" |
| `"GraphPayments"` | GraphPayments | Original |
| `"PaymentsEscrow"` | PaymentsEscrow | Original |
| `"EpochManager"` | MockEpochManager | Stub |
| `"RewardsManager"` | MockRewardsManager | Stub |
| `"GraphTokenGateway"` | MockTokenGateway | Stub |
| `"GraphProxyAdmin"` | MockProxyAdmin | Stub |
| `"Curation"` | MockCuration | Stub |

Current setup already registers "Staking" key pointing to MockStaking (line 415 in setup_test.go).

---

## Appendix B: Function Signature Changes

### authorizeSigner

```
// Mock (current)
authorizeSigner(address signer)
Selector: 0x... (1 param)

// Original
authorizeSigner(address signer, uint256 proofDeadline, bytes calldata proof)
Selector: 0x... (3 params)
```

### revokeSigner -> revokeAuthorizedSigner

```
// Mock (current)
revokeSigner(address signer)
Selector: 0x...

// Original (requires prior thawSigner call)
thawSigner(address signer)
Selector: 0x...

revokeAuthorizedSigner(address signer)
Selector: 0x...
```

### isAuthorized (unchanged signature, different logic)

```
// Both
isAuthorized(address authorizer, address signer) returns (bool)
Selector: 0x...

// Mock: returns (authorizer == signer || _authorized[authorizer][signer])
// Original: returns (authorizations[signer].authorizer == authorizer && !revoked)
```
