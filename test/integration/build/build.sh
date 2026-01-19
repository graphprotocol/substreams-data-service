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
contracts=(
    "GraphTallyVerifier"
    "MockGRTToken"
    "MockController"
    "MockStaking"
    "MockPaymentsEscrow"
    "MockGraphPayments"
    "MockEpochManager"
    "GraphTallyCollectorFull"
    "SubstreamsDataService"
)

# Extract artifacts
for contract in "${contracts[@]}"; do
    # Try different source file patterns
    ARTIFACT_PATH=""

    if [ -f "out/GraphTallyVerifier.sol/${contract}.json" ]; then
        ARTIFACT_PATH="out/GraphTallyVerifier.sol/${contract}.json"
    elif [ -f "out/IntegrationTestContracts.sol/${contract}.json" ]; then
        ARTIFACT_PATH="out/IntegrationTestContracts.sol/${contract}.json"
    elif [ -f "out/GraphTallyCollectorFull.sol/${contract}.json" ]; then
        ARTIFACT_PATH="out/GraphTallyCollectorFull.sol/${contract}.json"
    elif [ -f "out/SubstreamsDataService.sol/${contract}.json" ]; then
        ARTIFACT_PATH="out/SubstreamsDataService.sol/${contract}.json"
    elif [ -f "out/MockEpochManager.sol/${contract}.json" ]; then
        ARTIFACT_PATH="out/MockEpochManager.sol/${contract}.json"
    fi

    if [ -z "$ARTIFACT_PATH" ]; then
        echo "ERROR: Contract artifact not found for $contract"
        echo "Available artifacts:"
        find out -name "*.json" | head -20
        exit 1
    fi

    echo "Copying $contract from $ARTIFACT_PATH..."
    cp "$ARTIFACT_PATH" "/output/${contract}.json"
done

echo "Build complete!"
ls -la /output/
