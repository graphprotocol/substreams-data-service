FROM ghcr.io/foundry-rs/foundry:latest

USER root

RUN mkdir -p /build && chmod 777 /build
WORKDIR /build

# Clone horizon-contracts at specific commit
RUN git clone https://github.com/graphprotocol/contracts.git /horizon-contracts && \
    cd /horizon-contracts && \
    git checkout 41fd64b1f27bd9dd3fc5ca818eba63e4dcf6c73e

RUN forge init --no-git .

RUN forge install OpenZeppelin/openzeppelin-contracts@v5.1.0 --no-git
RUN forge install OpenZeppelin/openzeppelin-contracts-upgradeable@v5.1.0 --no-git

# Copy our contracts (mocks and data service only)
COPY contracts/TestMocks.sol ./src/
COPY contracts/SubstreamsDataService.sol ./src/

# Copy build script
COPY build.sh ./build.sh
RUN chmod +x ./build.sh

VOLUME /output

ENTRYPOINT ["./build.sh"]
