#!/bin/bash
set -e

echo "Building contract artifacts..."

cd /build

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
