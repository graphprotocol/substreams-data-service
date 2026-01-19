# Plan: Improve Integration Tests to Reuse Original Horizon Contracts

## Status: IMPLEMENTATION IN PROGRESS

**Last Updated**: 2026-01-19

**Completion**: 5 out of 9 phases completed (Phases 1, 4, 5, 6, and partial 8)

See `test/integration/IMPLEMENTATION_STATUS.md` for detailed status.

## ULTIMATE GOAL

Transition the horizon-go integration tests from using reimplemented/mock contracts to using the original contracts from the `horizon-contracts` submodule wherever practical. This includes:

1. Using the original `GraphTallyCollector`, `PaymentsEscrow`, and `GraphPayments` contracts
2. Creating a minimal but real `SubstreamsDataService` contract that extends `DataService` from horizon-contracts
3. Refactoring `TestEnv` to cache method definitions for cleaner, less verbose test code
4. Keeping only minimal mocks for components that would require deploying the entire Graph Protocol stack

---

## Executive Summary

| Aspect | Details |
|--------|---------|
| **Objective** | Replace reimplemented contracts with original horizon-contracts |
| **Recommended Approach** | Option A: Use original GraphTallyCollector + PaymentsEscrow + GraphPayments + SubstreamsDataService |
| **Complexity** | Medium-High (interface-compliant mocks + signer proof mechanism + DataService contract) |
| **Critical Findings** | 1) Original contract does NOT support self-authorization |
| | 2) PaymentsEscrow has 3-level mapping (payer->collector->receiver), our mock has 2-level |
| | 3) GraphPayments distributes payments with protocol cut, delegator cut, data service cut |
| **Original Contracts to Use** | GraphTallyCollector, PaymentsEscrow, GraphPayments, Authorizable, GraphDirectory, DataService |
| **Mocks Still Needed** | MockGRTToken, MockController, MockStaking |
| **New Contracts to Create** | SubstreamsDataService (extends DataService) |
| **Files Affected** | ~15 files (new: 6, modify: 8, delete: 2) |
| **Test Impact** | All tests need explicit signer authorization + correct deposit flow + DataService as caller |

---

## Summary

This plan outlines how to migrate the horizon-go integration tests from using reimplemented contracts to using the original contracts from the `horizon-contracts` submodule. The key changes are:

1. Use actual `GraphTallyCollector.sol` for EIP-712 and authorization
2. Use actual `PaymentsEscrow.sol` for correct 3-level mapping (payer->collector->receiver)
3. Use actual `GraphPayments.sol` for payment distribution (protocol cut, data service cut, delegator cut)
4. Create a minimal `SubstreamsDataService.sol` that extends `DataService` and integrates with `GraphTallyCollector`
5. Refactor `TestEnv` to cache method definitions for cleaner test code

**Key Trade-offs**:

1. **Signer Proof Mechanism**: Original `Authorizable` requires signers to prove consent via cryptographic proof
2. **No Self-Authorization**: Unlike our reimplementation, payers cannot sign their own RAVs without explicit authorization
3. **3-Level Escrow Mapping**: Deposits must specify (collector, receiver), not just payer
4. **DataService as Caller**: Only the DataService contract can call GraphTallyCollector.collect()
5. **More Dependencies**: GraphDirectory requires 10 contract stubs vs. our current 3

**Benefits**:
- Tests run against actual production contract code
- EIP-712 logic guaranteed to match production
- PaymentsEscrow mapping validated against production
- GraphPayments distribution logic exercised
- SubstreamsDataService provides a real data service implementation for future development
- Reduced maintenance burden when upstream changes
- Cleaner test code with cached method definitions

---

## Phase 1: SubstreamsDataService Contract Design

### 1.1 Overview

Create a minimal `SubstreamsDataService` contract that extends `DataService` from horizon-contracts. This contract:
- Will be a real contract intended for future development (not just for testing)
- Implements the strict minimum required for a DataService to work properly
- Integrates with `GraphTallyCollector` for payment collection (like `SubgraphService.sol`)
- Does NOT include allocation management, curation, or rewards distribution (unlike SubgraphService)

### 1.2 Contract Structure

**File**: `test/integration/build/contracts/SubstreamsDataService.sol`

```solidity
// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.27;

import { IGraphPayments } from "@graphprotocol/interfaces/contracts/horizon/IGraphPayments.sol";
import { IGraphTallyCollector } from "@graphprotocol/interfaces/contracts/horizon/IGraphTallyCollector.sol";
import { IDataService } from "@graphprotocol/interfaces/contracts/data-service/IDataService.sol";

import { Initializable } from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import { OwnableUpgradeable } from "@openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol";
import { DataService } from "@graphprotocol/horizon/contracts/data-service/DataService.sol";

/**
 * @title SubstreamsDataService
 * @notice A minimal data service contract for Substreams indexing and querying
 * @dev Implements the strict minimum required for a DataService to work with GraphTallyCollector
 */
contract SubstreamsDataService is
    Initializable,
    OwnableUpgradeable,
    DataService,
    IDataService
{
    /// @notice GraphTallyCollector address for payment collection
    IGraphTallyCollector public immutable GRAPH_TALLY_COLLECTOR;

    /// @notice Mapping of service provider to their registration status
    mapping(address => bool) public isRegistered;

    /// @notice Mapping of service provider to their payments destination
    mapping(address => address) public paymentsDestination;

    /// @notice Emitted when an indexer sets their payments destination
    event PaymentsDestinationSet(address indexed indexer, address indexed destination);

    /// @notice Error when indexer is not registered
    error SubstreamsDataServiceIndexerNotRegistered(address indexer);

    /// @notice Error when service provider in RAV doesn't match caller
    error SubstreamsDataServiceIndexerMismatch(address ravServiceProvider, address indexer);

    /// @notice Error when payment type is not supported
    error SubstreamsDataServiceInvalidPaymentType(IGraphPayments.PaymentTypes paymentType);

    /**
     * @notice Modifier to check if indexer is registered
     */
    modifier onlyRegisteredIndexer(address indexer) {
        require(isRegistered[indexer], SubstreamsDataServiceIndexerNotRegistered(indexer));
        _;
    }

    /**
     * @notice Constructor
     * @param controller The Graph Controller address
     * @param graphTallyCollector The GraphTallyCollector address
     */
    constructor(
        address controller,
        address graphTallyCollector
    ) DataService(controller) {
        GRAPH_TALLY_COLLECTOR = IGraphTallyCollector(graphTallyCollector);
        _disableInitializers();
    }

    /**
     * @notice Initialize the contract
     * @param owner The owner address
     * @param minimumProvisionTokens Minimum tokens required for provision
     */
    function initialize(
        address owner,
        uint256 minimumProvisionTokens
    ) external initializer {
        __Ownable_init(owner);
        __DataService_init();
        _setProvisionTokensRange(minimumProvisionTokens, type(uint256).max);
    }

    /// @inheritdoc IDataService
    function register(
        address indexer,
        bytes calldata data
    ) external override onlyAuthorizedForProvision(indexer) onlyValidProvision(indexer) {
        (address paymentsDestination_) = abi.decode(data, (address));

        isRegistered[indexer] = true;
        _setPaymentsDestination(indexer, paymentsDestination_);

        emit ServiceProviderRegistered(indexer, data);
    }

    /// @inheritdoc IDataService
    function acceptProvisionPendingParameters(
        address indexer,
        bytes calldata
    ) external override onlyAuthorizedForProvision(indexer) {
        _acceptProvisionParameters(indexer);
        emit ProvisionPendingParametersAccepted(indexer);
    }

    /// @inheritdoc IDataService
    function startService(address, bytes calldata) external pure override {
        // No-op for Substreams - service starts implicitly on registration
    }

    /// @inheritdoc IDataService
    function stopService(address, bytes calldata) external pure override {
        // No-op for Substreams - service stops implicitly on deregistration
    }

    /// @inheritdoc IDataService
    function collect(
        address indexer,
        IGraphPayments.PaymentTypes paymentType,
        bytes calldata data
    ) external override
      onlyAuthorizedForProvision(indexer)
      onlyValidProvision(indexer)
      onlyRegisteredIndexer(indexer)
      returns (uint256)
    {
        uint256 paymentCollected = 0;

        if (paymentType == IGraphPayments.PaymentTypes.QueryFee) {
            paymentCollected = _collectQueryFees(indexer, data);
        } else {
            revert SubstreamsDataServiceInvalidPaymentType(paymentType);
        }

        emit ServicePaymentCollected(indexer, paymentType, paymentCollected);
        return paymentCollected;
    }

    /// @inheritdoc IDataService
    function slash(address, bytes calldata) external pure override {
        // Slashing not implemented in minimal version
        // Would require DisputeManager integration
    }

    /**
     * @notice Set the payments destination for an indexer
     * @param paymentsDestination_ The destination address for payments
     */
    function setPaymentsDestination(address paymentsDestination_) external {
        _setPaymentsDestination(msg.sender, paymentsDestination_);
    }

    /**
     * @notice Collect query fees using GraphTallyCollector
     * @param indexer The indexer address
     * @param data Encoded SignedRAV
     * @return The amount of tokens collected
     */
    function _collectQueryFees(address indexer, bytes calldata data) private returns (uint256) {
        (IGraphTallyCollector.SignedRAV memory signedRav, uint256 dataServiceCut) = abi.decode(
            data,
            (IGraphTallyCollector.SignedRAV, uint256)
        );

        require(
            signedRav.rav.serviceProvider == indexer,
            SubstreamsDataServiceIndexerMismatch(signedRav.rav.serviceProvider, indexer)
        );

        // Collect via GraphTallyCollector
        uint256 tokensCollected = GRAPH_TALLY_COLLECTOR.collect(
            IGraphPayments.PaymentTypes.QueryFee,
            abi.encode(signedRav, dataServiceCut, paymentsDestination[indexer]),
            0 // collect full amount
        );

        return tokensCollected;
    }

    /**
     * @notice Internal function to set payments destination
     */
    function _setPaymentsDestination(address indexer, address destination) internal {
        paymentsDestination[indexer] = destination;
        emit PaymentsDestinationSet(indexer, destination);
    }
}
```

### 1.3 Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| **Extends DataService** | Inherits GraphDirectory, ProvisionManager - provides all protocol integration |
| **No Allocation Management** | Substreams doesn't use allocations - queries are paid directly |
| **No Curation** | Substreams doesn't have curation signals |
| **No Indexing Rewards** | Only QueryFee payment type supported initially |
| **No Dispute Manager** | Slashing not implemented in minimal version |
| **Direct Payment Model** | Payers pay indexers directly via RAVs, no gateway |
| **Immutable GraphTallyCollector** | Set at construction time like SubgraphService |

### 1.4 Tasks

- [x] Create `SubstreamsDataService.sol` in `test/integration/build/contracts/`
- [x] Add import remappings for `@graphprotocol/horizon/contracts/data-service/`
- [x] Update build system to compile SubstreamsDataService
- [ ] Deploy SubstreamsDataService in test setup (PENDING)
- [ ] Update tests to use SubstreamsDataService as the DataService caller (PENDING)

---

## Phase 2: Integrate Original GraphPayments Contract

### 2.1 Overview

Replace `MockGraphPayments` with the original `GraphPayments.sol` contract. This contract:
- Handles payment distribution from PaymentsEscrow
- Calculates and distributes protocol cut (burned), data service cut, delegator cut, and receiver amount
- Interacts with HorizonStaking for delegation pool management

### 2.2 GraphPayments Integration Requirements

The original GraphPayments contract requires:
1. **GraphDirectory dependencies** - needs Controller with registered contracts
2. **Protocol payment cut** - immutable value set at deployment
3. **HorizonStaking integration** - for delegation pool and staking operations

### 2.3 Payment Distribution Flow

```
PaymentsEscrow.collect()
    └── graphToken.approve(GraphPayments, tokens)
    └── GraphPayments.collect(paymentType, receiver, tokens, dataService, dataServiceCut, receiverDestination)
            │
            ├── tokensProtocol = tokens * PROTOCOL_PAYMENT_CUT (burned)
            ├── tokensDataService = (tokens - protocol) * dataServiceCut -> dataService
            ├── tokensDelegationPool = (remaining) * delegationFeeCut -> delegation pool
            └── tokensRemaining -> receiverDestination (or staked if destination=0)
```

### 2.4 Mock Updates Required

Since original GraphPayments interacts with HorizonStaking, our MockStaking needs additional methods:

```solidity
// Required by GraphPayments
function getDelegationPool(address serviceProvider, address dataService)
    external view returns (DelegationPool memory);

function getDelegationFeeCut(address serviceProvider, address dataService, PaymentTypes paymentType)
    external view returns (uint256);

function addToDelegationPool(address serviceProvider, address dataService, uint256 tokens) external;

function stakeTo(address serviceProvider, uint256 tokens) external;
```

### 2.5 Tasks

- [ ] Update MockStaking to implement required IHorizonStaking methods for GraphPayments
- [ ] Add GraphPayments deployment to test setup (after MockStaking is ready)
- [ ] Register GraphPayments in Controller under "GraphPayments" key
- [ ] Set appropriate PROTOCOL_PAYMENT_CUT for tests (e.g., 10000 = 1%)
- [ ] Update tests to verify payment distribution

---

## Phase 3: TestEnv Method Definition Caching

### 3.1 Problem Statement

Current test code is verbose due to repeated `abi.FindFunctionByName()` calls:

```go
// Current pattern - repeated in every helper function
func callMintGRT(..., abi *eth.ABI) error {
    mintFn := abi.FindFunctionByName("mint")
    if mintFn == nil {
        return fmt.Errorf("mint function not found in ABI")
    }
    data, err := mintFn.NewCall(to, amount).Encode()
    // ...
}
```

### 3.2 Proposed Solution

Cache method definitions in `TestEnv` at setup time with a clean `EncodeCall` API:

```go
// New structure for cached contract methods
type ContractMethods struct {
    Address eth.Address
    ABI     *eth.ABI
    methods map[string]*eth.Function // internal cache
}

type Contracts struct {
    GRTToken       *ContractMethods
    Controller     *ContractMethods
    Staking        *ContractMethods
    PaymentsEscrow *ContractMethods
    GraphPayments  *ContractMethods
    Collector      *ContractMethods
    DataService    *ContractMethods  // SubstreamsDataService
}

// Usage: env.Contracts.GRTToken.EncodeCall("mint", to, amount)
```

### 3.3 Usage Pattern

```go
// Before refactor - verbose, requires ABI parameter
func callMintGRT(ctx testContext, rpcURL string, key *eth.PrivateKey, chainID uint64,
    token eth.Address, to eth.Address, amount *big.Int, abi *eth.ABI) error {
    mintFn := abi.FindFunctionByName("mint")
    if mintFn == nil {
        return fmt.Errorf("mint function not found in ABI")
    }
    data, err := mintFn.NewCall(to, amount).Encode()
    if err != nil {
        return fmt.Errorf("encoding mint call: %w", err)
    }
    return sendTransaction(ctx, rpcURL, key, chainID, &token, big.NewInt(0), data)
}

// After refactor - clean, dynamic API
func (env *TestEnv) MintGRT(to eth.Address, amount *big.Int) error {
    data, err := env.Contracts.GRTToken.EncodeCall("mint", to, amount)
    if err != nil {
        return fmt.Errorf("encoding mint call: %w", err)
    }
    return env.SendTx(env.DeployerKey, env.Contracts.GRTToken.Address, data)
}

// Direct usage in tests is also clean:
data, _ := env.Contracts.GRTToken.EncodeCall("mint", to, amount)
data, _ := env.Contracts.Escrow.EncodeCall("deposit", collector, receiver, tokens)
data, _ := env.Contracts.Collector.EncodeCall("authorizeSigner", signer, deadline, proof)
```

### 3.4 Implementation Details

**File**: `test/integration/setup_test.go`

```go
// ContractMethods caches ABI and method definitions for a contract
type ContractMethods struct {
    Address eth.Address
    ABI     *eth.ABI
    methods map[string]*eth.Function // internal cache, not exported
}

// NewContractMethods creates a new ContractMethods with cached method lookups
func NewContractMethods(address eth.Address, abi *eth.ABI) *ContractMethods {
    methods := make(map[string]*eth.Function)
    for _, fn := range abi.Functions {
        methods[fn.Name] = fn
    }
    return &ContractMethods{
        Address: address,
        ABI:     abi,
        methods: methods,
    }
}

// EncodeCall encodes a method call with arguments
// This is the primary API for encoding contract calls
func (c *ContractMethods) EncodeCall(method string, args ...any) ([]byte, error) {
    fn, ok := c.methods[method]
    if !ok {
        return nil, fmt.Errorf("method %s not found in ABI", method)
    }
    return fn.NewCall(args...).Encode()
}

// MustEncodeCall is like EncodeCall but panics on error (useful in tests)
func (c *ContractMethods) MustEncodeCall(method string, args ...any) []byte {
    data, err := c.EncodeCall(method, args...)
    if err != nil {
        panic(fmt.Sprintf("MustEncodeCall %s: %v", method, err))
    }
    return data
}

// Contracts holds all deployed contracts with cached methods
type Contracts struct {
    GRTToken       *ContractMethods
    Controller     *ContractMethods
    Staking        *ContractMethods
    PaymentsEscrow *ContractMethods
    GraphPayments  *ContractMethods
    Collector      *ContractMethods
    DataService    *ContractMethods
}

// Updated TestEnv
type TestEnv struct {
    ctx            context.Context
    cancel         context.CancelFunc
    anvilContainer testcontainers.Container
    rpcURL         string
    ChainID        uint64

    // Keys
    DeployerKey         *eth.PrivateKey
    DeployerAddress     eth.Address
    ServiceProviderKey  *eth.PrivateKey
    ServiceProviderAddr eth.Address
    PayerKey            *eth.PrivateKey
    PayerAddr           eth.Address
    DataServiceKey      *eth.PrivateKey  // Deprecated - use Contracts.DataService.Address
    DataServiceAddr     eth.Address      // Deprecated - use Contracts.DataService.Address

    // Contracts with cached methods (NEW)
    Contracts *Contracts

    // Deprecated: Use Contracts.<Name>.Address instead
    GRTToken         eth.Address
    Controller       eth.Address
    Staking          eth.Address
    PaymentsEscrow   eth.Address
    CollectorAddress eth.Address

    // Deprecated: Use Contracts.<Name>.ABI instead
    ABIs *ABIs
}
```

### 3.5 Migration Path

1. **Phase 1**: Add `Contracts` field alongside existing fields (backward compatible)
2. **Phase 2**: Update helper functions to use new pattern
3. **Phase 3**: Mark old fields as deprecated
4. **Phase 4**: Remove deprecated fields in future cleanup

### 3.6 Tasks

- [ ] Add `ContractMethods` struct with `Method()` and `EncodeCall()` helpers
- [ ] Add `Contracts` struct to hold all contract methods
- [ ] Update `setupEnv()` to populate `Contracts` after deployment
- [ ] Create convenience methods on `TestEnv` for common operations
- [ ] Update existing test helpers to use new pattern
- [ ] Add deprecation comments to old fields
- [ ] Update tests to use new pattern where beneficial

---

## Phase 4: Interface-Compliant Mock Contracts (Updated)

### 4.1 Required Mocks

With the addition of original GraphPayments, our mocks need updates:

| Contract | Purpose | Key Changes Needed |
|----------|---------|-------------------|
| **MockGRTToken** | Simple ERC20 with public mint | Add `burnTokens()` for protocol cut |
| **MockController** | Contract registry | No changes - already sufficient |
| **MockStaking** | Provision tracking + delegation | Add delegation pool methods for GraphPayments |

### 4.2 Updated MockStaking

The MockStaking contract must implement the full `IHorizonStaking` interface methods used by `ProvisionManager` and `GraphPayments`. The key methods are:

```solidity
import { IHorizonStaking } from "@graphprotocol/interfaces/contracts/horizon/IHorizonStaking.sol";
import { IGraphPayments } from "@graphprotocol/interfaces/contracts/horizon/IGraphPayments.sol";

contract MockStaking {
    // Storage for provisions: serviceProvider => dataService => Provision
    struct ProvisionData {
        uint256 tokens;
        uint256 tokensThawing;
        uint256 sharesThawing;
        uint32 maxVerifierCut;
        uint64 thawingPeriod;
        uint64 createdAt;
        uint32 maxVerifierCutPending;
        uint64 thawingPeriodPending;
        uint256 lastParametersStagedAt;
        uint256 thawingNonce;
    }

    mapping(address => mapping(address => ProvisionData)) private _provisions;
    mapping(address => mapping(address => IHorizonStaking.DelegationPool)) private _delegationPools;
    mapping(address => mapping(address => mapping(IGraphPayments.PaymentTypes => uint256))) private _delegationFeeCuts;

    // Authorized operators: serviceProvider => dataService => operator => authorized
    mapping(address => mapping(address => mapping(address => bool))) private _operators;

    /**
     * @notice Set up a provision for testing
     * @dev This is a test helper - creates a provision with createdAt set
     */
    function setProvision(
        address serviceProvider,
        address dataService,
        uint256 tokens,
        uint32 maxVerifierCut,
        uint64 thawingPeriod
    ) external {
        _provisions[serviceProvider][dataService] = ProvisionData({
            tokens: tokens,
            tokensThawing: 0,
            sharesThawing: 0,
            maxVerifierCut: maxVerifierCut,
            thawingPeriod: thawingPeriod,
            createdAt: uint64(block.timestamp), // Mark as created
            maxVerifierCutPending: maxVerifierCut,
            thawingPeriodPending: thawingPeriod,
            lastParametersStagedAt: 0,
            thawingNonce: 0
        });
    }

    /**
     * @notice Authorize an operator for a service provider
     * @dev Required by ProvisionManager.onlyAuthorizedForProvision
     */
    function setOperator(address serviceProvider, address dataService, address operator, bool authorized) external {
        _operators[serviceProvider][dataService][operator] = authorized;
    }

    // --- IHorizonStaking interface methods ---

    /**
     * @notice Check if caller is authorized for provision
     * @dev Called by ProvisionManager.onlyAuthorizedForProvision modifier
     */
    function isAuthorized(address serviceProvider, address dataService, address caller)
        external view returns (bool) {
        // Service provider is always authorized for themselves
        if (caller == serviceProvider) return true;
        // Check explicit operator authorization
        return _operators[serviceProvider][dataService][caller];
    }

    /**
     * @notice Get provision for service provider
     * @dev Called by ProvisionManager._getProvision
     */
    function getProvision(address serviceProvider, address dataService)
        external view returns (IHorizonStaking.Provision memory) {
        ProvisionData storage p = _provisions[serviceProvider][dataService];
        return IHorizonStaking.Provision({
            tokens: p.tokens,
            tokensThawing: p.tokensThawing,
            sharesThawing: p.sharesThawing,
            maxVerifierCut: p.maxVerifierCut,
            thawingPeriod: p.thawingPeriod,
            createdAt: p.createdAt,
            maxVerifierCutPending: p.maxVerifierCutPending,
            thawingPeriodPending: p.thawingPeriodPending,
            lastParametersStagedAt: p.lastParametersStagedAt,
            thawingNonce: p.thawingNonce
        });
    }

    /**
     * @notice Accept provision parameters
     * @dev Called by ProvisionManager._acceptProvisionParameters
     */
    function acceptProvisionParameters(address serviceProvider) external {
        ProvisionData storage p = _provisions[serviceProvider][msg.sender];
        p.maxVerifierCut = p.maxVerifierCutPending;
        p.thawingPeriod = p.thawingPeriodPending;
    }

    /**
     * @notice Get available tokens (not thawing)
     * @dev Used by GraphTallyCollector to check provider has sufficient stake
     */
    function getProviderTokensAvailable(address serviceProvider, address verifier)
        external view returns (uint256) {
        ProvisionData storage p = _provisions[serviceProvider][verifier];
        return p.tokens - p.tokensThawing;
    }

    // --- Methods required by GraphPayments ---

    function getDelegationPool(address serviceProvider, address dataService)
        external view returns (IHorizonStaking.DelegationPool memory) {
        return _delegationPools[serviceProvider][dataService];
    }

    function getDelegationFeeCut(address serviceProvider, address dataService, IGraphPayments.PaymentTypes paymentType)
        external view returns (uint256) {
        return _delegationFeeCuts[serviceProvider][dataService][paymentType];
    }

    function addToDelegationPool(address serviceProvider, address dataService, uint256 tokens) external {
        _delegationPools[serviceProvider][dataService].tokens += tokens;
    }

    function stakeTo(address serviceProvider, uint256 tokens) external {
        _provisions[serviceProvider][msg.sender].tokens += tokens;
    }

    // Test helpers
    function setDelegationFeeCut(
        address serviceProvider,
        address dataService,
        IGraphPayments.PaymentTypes paymentType,
        uint256 cut
    ) external {
        _delegationFeeCuts[serviceProvider][dataService][paymentType] = cut;
    }
}
```

**Key Requirements for ProvisionManager Compatibility**:

1. `isAuthorized(serviceProvider, dataService, caller)` - Called by `onlyAuthorizedForProvision` modifier
2. `getProvision(serviceProvider, dataService)` - Called by `_getProvision()`, must return with `createdAt != 0`
3. `acceptProvisionParameters(serviceProvider)` - Called by `_acceptProvisionParameters()`

**Key Requirement**: The `getProvision()` return value MUST have `createdAt != 0` or `ProvisionManager` will revert with `ProvisionManagerProvisionNotFound`.

### 4.3 Tasks

- [x] Update MockGRTToken to add `burn()` and `burnFrom()` methods for protocol cut
- [x] Rewrite MockStaking with complete `Provision` struct support
- [x] Add `isAuthorized()` for ProvisionManager authorization checks
- [x] Add `getProvision()` returning full Provision struct with `createdAt != 0`
- [x] Add `acceptProvisionParameters()` for provision parameter acceptance
- [x] Add `getProviderTokensAvailable()` for GraphTallyCollector stake checks
- [x] Add delegation pool methods for GraphPayments (`getDelegationPool`, `getDelegationFeeCut`, `addToDelegationPool`)
- [x] Add `stakeTo()` for auto-staking when receiver destination is zero
- [x] Add test helper `setProvision()` and `setOperator()` for test setup
- [ ] Test that all mocks satisfy interface requirements with original contracts (PENDING - requires Docker)

---

## Phase 5: Build System Updates

### 5.1 Dockerfile Updates

```dockerfile
FROM ghcr.io/foundry-rs/foundry:latest

USER root
RUN mkdir -p /build && chmod 777 /build
WORKDIR /build

# Initialize foundry project
RUN forge init --no-git .

# Install OpenZeppelin
RUN forge install OpenZeppelin/openzeppelin-contracts@v5.1.0 --no-git
RUN forge install OpenZeppelin/openzeppelin-contracts-upgradeable@v5.1.0 --no-git

# Copy horizon-contracts submodule (mounted from host)
COPY horizon-contracts /horizon-contracts

# Create remappings
RUN cat > remappings.txt << 'EOF'
@openzeppelin/contracts/=lib/openzeppelin-contracts/contracts/
@openzeppelin/contracts-upgradeable/=lib/openzeppelin-contracts-upgradeable/contracts/
@graphprotocol/interfaces/=/horizon-contracts/packages/interfaces/
@graphprotocol/horizon/=/horizon-contracts/packages/horizon/
@graphprotocol/contracts/=/horizon-contracts/packages/contracts/
EOF

# Copy contracts
COPY contracts/ ./src/

COPY build.sh ./build.sh
RUN chmod +x ./build.sh

VOLUME /output
ENTRYPOINT ["./build.sh"]
```

### 5.2 Remappings

```
@openzeppelin/contracts/=lib/openzeppelin-contracts/contracts/
@openzeppelin/contracts-upgradeable/=lib/openzeppelin-contracts-upgradeable/contracts/
@graphprotocol/interfaces/=/horizon-contracts/packages/interfaces/
@graphprotocol/horizon/=/horizon-contracts/packages/horizon/
@graphprotocol/contracts/=/horizon-contracts/packages/contracts/
```

### 5.3 Contracts to Compile

| Contract | Source | Notes |
|----------|--------|-------|
| MockGRTToken | Local mock | Simple ERC20 |
| MockController | Local mock | Contract registry |
| MockStaking | Local mock | Provision + delegation tracking |
| GraphTallyCollector | Original | From horizon-contracts |
| PaymentsEscrow | Original | From horizon-contracts |
| GraphPayments | Original | From horizon-contracts |
| SubstreamsDataService | Local | Extends DataService |

### 5.4 Tasks

- [x] Update Dockerfile to copy horizon-contracts submodule
- [x] Add OpenZeppelin upgradeable contracts dependency
- [x] Update remappings.txt with all required paths
- [x] Update build.sh to compile all contracts
- [ ] Test build produces all required artifacts (PENDING - requires Docker)

---

## Phase 6: Test Infrastructure Updates

### 6.1 Deployment Order

The deployment order must respect contract dependencies:

1. **MockGRTToken** - No dependencies
2. **MockController** - Takes governor address
3. **MockStaking** - No dependencies (mock)
4. **GraphPayments** - Requires Controller + PROTOCOL_PAYMENT_CUT
5. **PaymentsEscrow** - Requires Controller + thawing period
6. **GraphTallyCollector** - Requires Controller + thawing period
7. **SubstreamsDataService** - Requires Controller + GraphTallyCollector

After deployment, register in Controller:
- GraphToken
- HorizonStaking
- GraphPayments
- PaymentsEscrow

### 6.2 Test Setup Changes

```go
func setupEnv() (*TestEnv, error) {
    // ... container setup ...

    // Deploy contracts in dependency order
    grtAddr := deployMockGRTToken(...)
    controllerAddr := deployMockController(...)
    stakingAddr := deployMockStaking(...)

    // Register in Controller
    registerContract(controllerAddr, "GraphToken", grtAddr)
    registerContract(controllerAddr, "HorizonStaking", stakingAddr)

    // Deploy original contracts
    graphPaymentsAddr := deployGraphPayments(controllerAddr, protocolCut)
    escrowAddr := deployPaymentsEscrow(controllerAddr, thawingPeriod)
    collectorAddr := deployGraphTallyCollector(controllerAddr, thawingPeriod)

    // Register payment contracts
    registerContract(controllerAddr, "GraphPayments", graphPaymentsAddr)
    registerContract(controllerAddr, "PaymentsEscrow", escrowAddr)

    // Deploy SubstreamsDataService
    dataServiceAddr := deploySubstreamsDataService(controllerAddr, collectorAddr)

    // Initialize SubstreamsDataService
    initSubstreamsDataService(dataServiceAddr, deployerAddr, minProvisionTokens)

    // Create Contracts with cached methods
    contracts := &Contracts{
        GRTToken:       NewContractMethods(grtAddr, grtABI),
        Controller:     NewContractMethods(controllerAddr, controllerABI),
        Staking:        NewContractMethods(stakingAddr, stakingABI),
        PaymentsEscrow: NewContractMethods(escrowAddr, escrowABI),
        GraphPayments:  NewContractMethods(graphPaymentsAddr, graphPaymentsABI),
        Collector:      NewContractMethods(collectorAddr, collectorABI),
        DataService:    NewContractMethods(dataServiceAddr, dataServiceABI),
    }

    return &TestEnv{
        // ...
        Contracts: contracts,
    }, nil
}
```

### 6.3 Test Flow Changes

With SubstreamsDataService, the test flow changes:

**Before** (DataService was just an EOA):
```go
// DataService key signs transaction directly to GraphTallyCollector
callCollect(env.DataServiceKey, env.CollectorAddress, signedRAV, ...)
```

**After** (DataService is a contract):
```go
// 1. Register indexer with SubstreamsDataService
env.RegisterIndexer(env.ServiceProviderKey, env.ServiceProviderAddr, paymentsDestination)

// 2. Call SubstreamsDataService.collect() which internally calls GraphTallyCollector
env.CollectViaDataService(env.ServiceProviderKey, signedRAV, dataServiceCut)
```

### 6.4 Tasks

- [ ] Update setupEnv() with new deployment order (PENDING)
- [ ] Add GraphPayments deployment with protocol cut configuration (SKIPPED - Phase 2 not implemented)
- [ ] Add SubstreamsDataService deployment and initialization (PENDING)
- [ ] Update contract registration in Controller (PENDING)
- [ ] Add helper methods for DataService interactions (PENDING)
- [x] Update helper function signatures (callDepositEscrow, callSetProvision)

---

## Phase 7: Signer Proof Implementation

### 7.1 Overview

The original `Authorizable.sol` requires a cryptographic proof when authorizing a signer. This ensures the signer consents to being authorized.

### 7.2 Proof Generation

```go
// GenerateSignerProof generates a proof for authorizing a signer
func GenerateSignerProof(
    chainID uint64,
    collectorAddress eth.Address,
    proofDeadline uint64,
    authorizer eth.Address,
    signerKey *eth.PrivateKey,
) ([]byte, error) {
    // abi.encodePacked(block.chainid, address(this), "authorizeSignerProof", _proofDeadline, msg.sender)
    message := make([]byte, 0, 124) // 32+20+20+32+20

    // chainId as uint256 (32 bytes)
    chainIDBytes := make([]byte, 32)
    new(big.Int).SetUint64(chainID).FillBytes(chainIDBytes)
    message = append(message, chainIDBytes...)

    // contractAddress (20 bytes)
    message = append(message, collectorAddress[:]...)

    // "authorizeSignerProof" (20 bytes)
    message = append(message, []byte("authorizeSignerProof")...)

    // proofDeadline as uint256 (32 bytes)
    deadlineBytes := make([]byte, 32)
    new(big.Int).SetUint64(proofDeadline).FillBytes(deadlineBytes)
    message = append(message, deadlineBytes...)

    // authorizer address (20 bytes)
    message = append(message, authorizer[:]...)

    // Hash and sign
    messageHash := eth.Keccak256(message)
    prefix := []byte("\x19Ethereum Signed Message:\n32")
    digest := eth.Keccak256(append(prefix, messageHash...))

    return signerKey.Sign(digest)
}
```

### 7.3 Tasks

- [ ] Implement `GenerateSignerProof()` function
- [ ] Implement `callAuthorizeSignerWithProof()` helper
- [ ] Create `AuthorizeSelf()` convenience function for tests
- [ ] Update authorization tests to use proof mechanism
- [ ] Add tests for proof validation edge cases

---

## Phase 8: Test Migration

### 8.1 Test Updates Required

| Test File | Changes Needed |
|-----------|---------------|
| `collect_test.go` | Use DataService.collect(), update deposit flow |
| `authorization_test.go` | Add signer proof mechanism |
| `rav_test.go` | Verify compatibility with new setup |

### 8.2 Key Changes

1. **Deposit Flow**: Use 3-level mapping `deposit(collector, receiver, tokens)`
2. **Authorization**: Generate and use signer proofs
3. **Collection**: Call via SubstreamsDataService instead of directly to Collector
4. **Payment Verification**: Verify distribution to protocol, data service, delegators, receiver

### 8.3 Tasks

- [x] Update `callDepositEscrow()` to use correct 3-level deposit
- [x] Update `callSetProvision()` to use new signature with maxVerifierCut and thawingPeriod
- [x] Update all test calls in collect_test.go (2 tests)
- [x] Update all test calls in authorization_test.go (3 tests)
- [ ] Update `callCollect()` to go through SubstreamsDataService (PENDING - requires deployment)
- [ ] Add authorization proof to `callAuthorizeSigner()` (SKIPPED - Phase 7 not implemented)
- [ ] Add payment distribution verification tests (PENDING)
- [ ] Add SubstreamsDataService registration tests (PENDING)
- [ ] Check and update rav_test.go if needed (PENDING)

---

## Phase 9: Documentation & Cleanup

### 9.1 Tasks

- [ ] Update `docs/contracts.md` with new architecture
- [ ] Document SubstreamsDataService contract and its intended use
- [ ] Remove deprecated reimplemented contracts
- [ ] Add inline comments explaining mock vs original contracts
- [ ] Create migration guide for existing test patterns

---

## Files to Change

| File | Change Type | Description |
|------|-------------|-------------|
| `test/integration/build/contracts/SubstreamsDataService.sol` | **New** | Minimal DataService implementation |
| `test/integration/build/contracts/MockProtocolContracts.sol` | **New** | Updated mocks for GraphPayments compatibility |
| `test/integration/build/Dockerfile` | Modify | Add horizon-contracts mounting and dependencies |
| `test/integration/build/remappings.txt` | Modify | Add all required import paths |
| `test/integration/build/build.sh` | Modify | Compile all contracts including originals |
| `test/integration/setup_test.go` | Modify | New deployment order, Contracts caching, DataService setup |
| `test/integration/helpers_test.go` | **New** | Extracted helper functions with new patterns |
| `test/integration/authorization_test.go` | Modify | Add signer proof mechanism |
| `test/integration/collect_test.go` | Modify | Use DataService.collect(), verify payment distribution |
| `test/integration/rav_test.go` | Review | Verify compatibility |
| `docs/contracts.md` | Modify | Document new architecture |
| `test/integration/build/contracts/GraphTallyCollectorFull.sol` | **Delete** | Replaced by original |
| `test/integration/build/contracts/IntegrationTestContracts.sol` | **Delete** | Replaced by new mocks |

---

## Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| GraphDirectory requires many contracts | Create stub implementations that satisfy constructor |
| Signer proof mechanism complexity | Create helper function to generate valid proofs |
| Breaking changes in horizon-contracts | Pin submodule to specific commit |
| Build time increase | Cache compiled contracts |
| Self-authorization not supported | All tests must explicitly authorize signers |
| SubstreamsDataService complexity | Start with minimal implementation, extend later |
| GraphPayments delegation pool interactions | Mock delegation pool with simple implementation |

---

## Success Criteria

1. Integration tests pass using original GraphTallyCollector, PaymentsEscrow, GraphPayments
2. SubstreamsDataService successfully collects payments via GraphTallyCollector
3. Payment distribution (protocol cut, data service cut) is verified
4. TestEnv provides clean API with cached method definitions
5. EIP-712 compatibility verified against original implementation
6. Clear documentation of mock vs. original contracts
7. Reduced maintenance surface area

---

## Next Steps

1. [ ] Review this plan and get feedback
2. [ ] **Phase 1**: Create SubstreamsDataService contract
3. [ ] **Phase 4**: Update mock contracts for GraphPayments compatibility
4. [ ] **Phase 5**: Update build system
5. [ ] **Phase 2**: Integrate GraphPayments
6. [ ] **Phase 3**: Refactor TestEnv with method caching
7. [ ] **Phase 6**: Update test infrastructure
8. [ ] **Phase 7**: Implement signer proof mechanism
9. [ ] **Phase 8**: Migrate tests
10. [ ] **Phase 9**: Documentation and cleanup

---

## Appendix A: Contract Dependency Graph

```
                    MockController
                         │
         ┌───────────────┼───────────────────┐
         │               │                   │
         ▼               ▼                   ▼
    MockGRTToken    MockStaking        GraphDirectory
         │               │                   │
         │               │     ┌─────────────┴─────────────┐
         │               │     │             │             │
         ▼               ▼     ▼             ▼             ▼
    PaymentsEscrow◄──GraphPayments    GraphTallyCollector
         │                                   │
         └───────────────┬───────────────────┘
                         │
                         ▼
               SubstreamsDataService
                         │
                         ▼
                   (Test Calls)
```

---

## Appendix B: Payment Flow with All Original Contracts

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        COMPLETE PAYMENT COLLECTION FLOW                          │
└─────────────────────────────────────────────────────────────────────────────────┘

1. SETUP: Payer deposits tokens for specific collector+receiver
   ┌──────────┐     deposit(collector, receiver, tokens)     ┌───────────────┐
   │  Payer   │ ────────────────────────────────────────────>│PaymentsEscrow │
   └──────────┘                                              └───────────────┘
                                                                    │
                escrowAccounts[payer][collector][receiver].balance += tokens

2. AUTHORIZE: Payer authorizes signer (with proof)
   ┌──────────┐     authorizeSigner(signer, deadline, proof) ┌────────────────────┐
   │  Payer   │ ────────────────────────────────────────────>│GraphTallyCollector │
   └──────────┘                                              └────────────────────┘

3. COLLECTION: Indexer calls SubstreamsDataService.collect()
   ┌──────────┐   collect(indexer, QueryFee, data)   ┌─────────────────────┐
   │ Indexer  │ ────────────────────────────────────>│SubstreamsDataService│
   └──────────┘                                      └─────────────────────┘
                                                              │
   ┌─────────────────────┐                                    │
   │SubstreamsDataService│<───────────────────────────────────┘
   │   (msg.sender)      │
   └─────────────────────┘
            │
            │ GRAPH_TALLY_COLLECTOR.collect(QueryFee, data)
            ▼
   ┌────────────────────┐
   │GraphTallyCollector │ Verifies: RAV signature, signer authorization, provision
   └────────────────────┘
            │
            │ escrow.collect(QueryFee, payer, receiver, tokens, dataService, cut, dest)
            ▼
   ┌───────────────┐
   │PaymentsEscrow │ Looks up: escrowAccounts[payer][msg.sender][receiver]
   └───────────────┘                          │
            │                                 └── msg.sender = GraphTallyCollector
            │
            │ graphToken.approve(GraphPayments, tokens)
            │ graphPayments.collect(...)
            ▼
   ┌───────────────┐
   │ GraphPayments │ Distributes:
   └───────────────┘
            │
            ├── tokensProtocol ────────────────> BURNED
            ├── tokensDataService ─────────────> SubstreamsDataService
            ├── tokensDelegationPool ──────────> Delegation Pool (via MockStaking)
            └── tokensRemaining ───────────────> receiverDestination (or staked)
```

---

## Appendix C: SubstreamsDataService vs SubgraphService Comparison

| Feature | SubgraphService | SubstreamsDataService |
|---------|-----------------|----------------------|
| **Purpose** | Subgraph indexing & querying | Substreams indexing & querying |
| **Allocations** | Yes - allocate tokens to subgraphs | No - no allocation concept |
| **Curation** | Yes - curation fees collected | No - no curation |
| **Indexing Rewards** | Yes - POI-based rewards | No - query fees only |
| **Dispute Manager** | Yes - slashing support | No - not in minimal version |
| **Payment Types** | QueryFee, IndexingRewards | QueryFee only |
| **Gateway** | Yes - aggregates receipts | No - direct payer-indexer |
| **RAV Model** | Gateway signs RAVs | Payer signs RAVs directly |
| **Registration** | URL, geohash, payments dest | Payments destination only |
| **Complexity** | ~600 lines | ~150 lines |

---

## Appendix D: TestEnv Method Caching Example

```go
// Complete example of refactored test code using EncodeCall

// Before: Verbose, requires ABI parameter, many arguments
func callMintGRT(ctx testContext, rpcURL string, key *eth.PrivateKey, chainID uint64,
    token eth.Address, to eth.Address, amount *big.Int, abi *eth.ABI) error {
    mintFn := abi.FindFunctionByName("mint")
    if mintFn == nil {
        return fmt.Errorf("mint function not found in ABI")
    }
    data, err := mintFn.NewCall(to, amount).Encode()
    if err != nil {
        return fmt.Errorf("encoding mint call: %w", err)
    }
    return sendTransaction(ctx, rpcURL, key, chainID, &token, big.NewInt(0), data)
}

// After: Clean helper using EncodeCall
func (env *TestEnv) MintGRT(to eth.Address, amount *big.Int) error {
    data, err := env.Contracts.GRTToken.EncodeCall("mint", to, amount)
    if err != nil {
        return fmt.Errorf("encoding mint call: %w", err)
    }
    return env.SendTx(env.DeployerKey, env.Contracts.GRTToken.Address, data)
}

// Test code becomes much cleaner
func TestCollectRAV(t *testing.T) {
    env := SetupEnv(t)

    // Before: Many parameters, ABI passing
    err := callMintGRT(env.ctx, env.rpcURL, env.DeployerKey, env.ChainID,
        env.GRTToken, env.PayerAddr, amount, env.ABIs.GRTToken)

    // After: Clean, obvious
    err := env.MintGRT(env.PayerAddr, amount)

    // Direct EncodeCall usage in tests is also clean:
    data, _ := env.Contracts.Escrow.EncodeCall("deposit", collector, receiver, tokens)
    env.SendTx(env.PayerKey, env.Contracts.Escrow.Address, data)

    // Or use MustEncodeCall in tests where panicking on error is acceptable:
    data := env.Contracts.Collector.MustEncodeCall("authorizeSigner", signer, deadline, proof)
}
```
