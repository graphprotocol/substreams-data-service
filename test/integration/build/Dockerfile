FROM ghcr.io/foundry-rs/foundry:latest

# Run as root to avoid permission issues
USER root

# Create a build directory
RUN mkdir -p /build && chmod 777 /build
WORKDIR /build

# Initialize foundry project first (in empty directory)
RUN forge init --no-git .

# Install OpenZeppelin contracts dependencies
RUN forge install OpenZeppelin/openzeppelin-contracts@v5.1.0 --no-git
RUN forge install OpenZeppelin/openzeppelin-contracts-upgradeable@v5.1.0 --no-git

# Copy our contracts
# TestMocks.sol: Contains MOCK contracts (MockGRTToken, MockController, MockStaking, stubs)
# OriginalContracts.sol: Imports ORIGINAL contracts from horizon-contracts (PaymentsEscrow, GraphPayments, GraphTallyCollector)
# SubstreamsDataService.sol: Our data service contract
COPY contracts/TestMocks.sol ./src/
COPY contracts/OriginalContracts.sol ./src/
COPY contracts/SubstreamsDataService.sol ./src/

# Copy build script
COPY build.sh ./build.sh
RUN chmod +x ./build.sh

# Output directory will be mounted
VOLUME /output

ENTRYPOINT ["./build.sh"]
