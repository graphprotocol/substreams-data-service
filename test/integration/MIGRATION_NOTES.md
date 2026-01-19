# Integration Tests Migration Notes

## Changes Made

### Phase 1: SubstreamsDataService Contract (COMPLETED)
- Created SubstreamsDataService.sol extending DataService from horizon-contracts
- Implements minimal data service for Substreams with GraphTallyCollector integration
- Located at: test/integration/build/contracts/SubstreamsDataService.sol

### Phase 4: Updated Mock Contracts (COMPLETED)
- **MockGRTToken**: Added burn() and burnFrom() methods for protocol cut
- **MockStaking**: Complete rewrite with full Provision struct support
  - Implements IHorizonStaking methods for ProvisionManager compatibility
  - Added delegation pool methods for GraphPayments
  - Added operator authorization support
  - setProvision() now requires: serviceProvider, dataService, tokens, maxVerifierCut, thawingPeriod
- **MockPaymentsEscrow**: Updated to 3-level mapping (payer -> collector -> receiver)
  - deposit() now requires: collector, receiver, amount
  - getEscrowAmount() now requires: payer, collector, receiver

### Phase 5: Build System Updates (COMPLETED)
- Updated Dockerfile to mount horizon-contracts submodule
- Added OpenZeppelin upgradeable contracts dependency
- Created comprehensive remappings for @graphprotocol imports
- Updated build.sh to compile SubstreamsDataService
- Modified main_test.go to mount horizon-contracts directory

### Phase 6: Test Infrastructure Updates (IN PROGRESS)
- Need to update setup_test.go deployment order
- Need to add SubstreamsDataService deployment
- Need to update helper functions for new contract signatures

### Remaining Phases
- Phase 3: TestEnv Method Caching (would improve test code)
- Phase 7: Signer Proof Implementation (required for authorization)
- Phase 8: Test Migration (update existing tests)
- Phase 9: Documentation & Cleanup

## Breaking Changes

### Mock Contract API Changes

#### MockPaymentsEscrow.deposit()
**Before:**
```solidity
function deposit(address sender, uint256 amount) external
```

**After:**
```solidity  
function deposit(address collector, address receiver, uint256 amount) external
```

**Migration:** Tests must specify collector (GraphTallyCollector) and receiver (service provider) explicitly.

#### MockStaking.setProvision()
**Before:**
```solidity
function setProvision(address serviceProvider, address dataService, uint256 tokens) external
```

**After:**
```solidity
function setProvision(
    address serviceProvider,
    address dataService,
    uint256 tokens,
    uint32 maxVerifierCut,
    uint64 thawingPeriod
) external
```

**Migration:** Tests must provide maxVerifierCut and thawingPeriod values.

## Next Steps

1. Update setup_test.go to deploy SubstreamsDataService
2. Update callDepositEscrow() to use 3-level deposit
3. Update callSetProvision() to use new signature
4. Update tests to pass collector/receiver to deposits
5. Implement signer proof mechanism for authorization
6. Test full flow end-to-end
