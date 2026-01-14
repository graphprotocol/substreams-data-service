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
