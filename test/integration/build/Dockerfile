FROM ghcr.io/foundry-rs/foundry:latest

# Run as root to avoid permission issues
USER root

# Create a build directory
RUN mkdir -p /build && chmod 777 /build
WORKDIR /build

# Initialize foundry project first (in empty directory)
RUN forge init --no-git .

# Install OpenZeppelin contracts dependency
RUN forge install OpenZeppelin/openzeppelin-contracts@v5.1.0 --no-git

# Create remappings for OpenZeppelin
RUN echo '@openzeppelin/contracts/=lib/openzeppelin-contracts/contracts/' > remappings.txt

# Now copy our test contracts (will overwrite the default src files)
COPY contracts/GraphTallyVerifier.sol ./src/
COPY contracts/IntegrationTestContracts.sol ./src/
COPY contracts/GraphTallyCollectorFull.sol ./src/

# Copy build script
COPY build.sh ./build.sh
RUN chmod +x ./build.sh

# Output directory will be mounted
VOLUME /output

ENTRYPOINT ["./build.sh"]
