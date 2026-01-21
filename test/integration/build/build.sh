#!/bin/bash
set -e

echo "Building contract artifacts..."

cd /build

# Create remappings.txt with horizon-contracts paths (mounted at runtime)
cat > remappings.txt << 'EOF'
@openzeppelin/contracts/=lib/openzeppelin-contracts/contracts/
@openzeppelin/contracts-upgradeable/=lib/openzeppelin-contracts-upgradeable/contracts/
@graphprotocol/interfaces/=/horizon-contracts/packages/interfaces/
@graphprotocol/horizon/=/horizon-contracts/packages/horizon/
@graphprotocol/contracts/=/horizon-contracts/packages/contracts/
EOF

echo "Verifying horizon-contracts mount..."
if [ ! -d "/horizon-contracts/packages/interfaces" ]; then
    echo "ERROR: horizon-contracts not properly mounted at /horizon-contracts"
    ls -la /horizon-contracts/ || echo "Mount point does not exist"
    exit 1
fi
echo "horizon-contracts mount verified successfully"

# Build contracts
forge build

# List of contracts to extract
# MOCKS (from TestMocks.sol) - These are our test infrastructure
# ORIGINAL (from horizon-contracts via OriginalContracts.sol) - These are the real contracts we test against
contracts=(
    # Original contracts from horizon-contracts (via OriginalContracts.sol)
    "PaymentsEscrow"
    "GraphPayments"
    "GraphTallyCollector"

    # Mock infrastructure (from TestMocks.sol)
    "MockGRTToken"
    "MockController"
    "MockStaking"
    "MockEpochManager"
    "MockRewardsManager"
    "MockTokenGateway"
    "MockProxyAdmin"
    "MockCuration"

    # Our data service contract
    "SubstreamsDataService"
)

# Extract artifacts
for contract in "${contracts[@]}"; do
    ARTIFACT_PATH=""

    # Check all possible source file locations
    # IMPORTANT: Prefer TestMocks.sol for Mock* contracts since they have full implementations
    if [ -f "out/TestMocks.sol/${contract}.json" ]; then
        ARTIFACT_PATH="out/TestMocks.sol/${contract}.json"
    elif [ -f "out/OriginalContracts.sol/${contract}.json" ]; then
        ARTIFACT_PATH="out/OriginalContracts.sol/${contract}.json"
    elif [ -f "out/SubstreamsDataService.sol/${contract}.json" ]; then
        ARTIFACT_PATH="out/SubstreamsDataService.sol/${contract}.json"
    # Original contracts may be compiled under their own source file names
    elif [ -f "out/PaymentsEscrow.sol/${contract}.json" ]; then
        ARTIFACT_PATH="out/PaymentsEscrow.sol/${contract}.json"
    elif [ -f "out/GraphPayments.sol/${contract}.json" ]; then
        ARTIFACT_PATH="out/GraphPayments.sol/${contract}.json"
    elif [ -f "out/GraphTallyCollector.sol/${contract}.json" ]; then
        ARTIFACT_PATH="out/GraphTallyCollector.sol/${contract}.json"
    fi

    if [ -z "$ARTIFACT_PATH" ]; then
        echo "ERROR: Contract artifact not found for $contract"
        echo "Available artifacts:"
        find out -name "*.json" | head -30
        exit 1
    fi

    echo "Copying $contract from $ARTIFACT_PATH..."
    cp "$ARTIFACT_PATH" "/output/${contract}.json"
done

echo ""
echo "Build complete!"
echo "ORIGINAL contracts (from horizon-contracts): PaymentsEscrow, GraphPayments, GraphTallyCollector"
echo "MOCK contracts (test infrastructure): MockGRTToken, MockController, MockStaking, etc."
echo ""
ls -la /output/
