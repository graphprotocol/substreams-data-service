FROM node:20-alpine

WORKDIR /app

# Install dependencies
RUN apk add --no-cache git curl bash jq

# Install foundry
RUN curl -L https://foundry.paradigm.xyz | bash && \
    /root/.foundry/bin/foundryup

ENV PATH="/root/.foundry/bin:${PATH}"

# Clone Graph Protocol contracts (Horizon)
RUN git clone --depth 1 https://github.com/graphprotocol/contracts.git /app/contracts

WORKDIR /app/contracts

# Install npm dependencies and build
RUN npm install
RUN forge build

# Copy build script
COPY build.sh /app/build.sh
RUN chmod +x /app/build.sh

# Output directory will be mounted
VOLUME /output

ENTRYPOINT ["/app/build.sh"]
