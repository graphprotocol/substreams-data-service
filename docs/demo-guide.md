# Demo Guide

This guide walks you through running the full Substreams Data Service stack locally — a real end-to-end paid Substreams session on a local chain with real contract interactions.

By the end you will have:
- A local Anvil chain with deployed TAP/Horizon contracts and pre-funded escrow
- A running Provider Gateway, Consumer Sidecar, and Firehose Core instance
- A Substreams client streaming data through the full payment loop

---

## Prerequisites

Install the following before starting.

### Go 1.25+

```bash
go version  # must be >= 1.25
```

### Docker

Docker is used for PostgreSQL, Redis, and pgweb. Make sure `docker compose` (v2) is available:

```bash
docker compose version
```

### reflex

Used to orchestrate the dev stack with auto-restart on code changes:

```bash
go install github.com/cespare/reflex@latest
```

### dummy-blockchain

A lightweight fake blockchain used by Firehose Core in the dev environment:

```bash
go install github.com/streamingfast/dummy-blockchain@latest
```

### firecore

The Firehose Core binary. Follow the [firecore installation instructions](https://github.com/streamingfast/firehose-core) for your platform.

> **Note:** The published `ghcr.io/streamingfast/dummy-blockchain:v1.7.7` Docker image may be stale for the current SDS plugin contract. For integration test (`TestFirecore`) validation, build local `firehose-core` and `dummy-blockchain` images from sibling checkouts — see the [runtime compatibility doc](provider-runtime-compatibility.md). For the `reflex -c .reflex` demo flow using a local `firecore` binary, the above is not required.

```bash
firecore --version  # verify it's installed
```

### substreams CLI

Used to stream data through the running stack:

```bash
# Installation: https://docs.substreams.dev/how-to-guides/installing-the-cli
substreams --version  # verify it's installed
```

### direnv (optional but recommended)

Automatically loads `.envrc` when you enter the directory:

```bash
brew install direnv   # macOS
# or: https://direnv.net/docs/installation.html
```

---

## 1. Clone and set up

```bash
git clone https://github.com/graphprotocol/substreams-data-service
cd substreams-data-service

# Check out the MVP scope branch
git checkout juanmardefago/mvp-scope
```

### Set up your PATH (with direnv)

Create an `.envrc` file in the repo root:

```bash
cat > .envrc << 'EOF'
PATH_add "$(pwd)/devel"
PATH_add "$(go env GOPATH)/bin"
EOF

direnv allow
```

This adds the `devel/` wrapper scripts and your Go bin to `$PATH`, so you can run `sds` directly.

> **Without direnv:** prefix commands with `./devel/` (e.g. `./devel/sds devenv`) and make sure `$GOPATH/bin` is in your `$PATH`.

---

## 2. Run the full stack (recommended)

The easiest way to run everything is with `reflex`, which starts all services and restarts them automatically when you change Go or SQL files:

```bash
reflex -c .reflex
```

That's it. Reflex orchestrates the following in order:

| Step | What runs | Where |
|------|-----------|-------|
| 1 | Docker Compose — PostgreSQL, Redis, pgweb | Background |
| 2 | Database migrations | One-shot |
| 3 | `sds devenv` — Anvil chain + contract deployment | Foreground |
| 4 | Consumer Sidecar (direct provider ingress) | `:9002` |
| 5 | Provider Gateway | `:9001` (control plane), `:9003` (plugin) |
| 6 | Firehose Core | `:10016` (Substreams), `:10015` (Firehose) |

Wait until you see the Firehose Core startup banner before proceeding to step 3.

**Expected output (abridged):**

```
Starting Docker Compose (PostgreSQL, Redis, pgweb)...
Starting Devenv...
[devenv] Anvil started on http://localhost:58545
[devenv] Contracts deployed.
[devenv] Demo escrow funded. Ready.
Starting Consumer Sidecar (direct provider ingress) ...
Starting Provider Gateway (with PostgreSQL)...
Restarting firehose-core instance...
 Consumer sidecar ingress: http://localhost:9002
 Provider control plane:   http://localhost:9001
 Substreams tier1:         https://localhost:10016?insecure=true
```

> **pgweb** is available at [http://localhost:8081](http://localhost:8081) — you can inspect the PostgreSQL database in your browser as sessions and RAVs are created.

---

## 3. Stream data through the payment loop

Once the stack is running, use the `substreams` CLI to run a package against the consumer sidecar on `:9002`:

```bash
# Stream 20 blocks from block 0 and exit
substreams run common@v0.1.0 map_clocks \
  -e localhost:9002 \
  --plaintext \
  -s 0 \
  -t +20
```

Or use the interactive GUI mode (streams continuously):

```bash
substreams gui common@v0.1.0 map_clocks \
  -e localhost:9002 \
  --plaintext
```

The consumer sidecar on `:9002` is the Substreams-compatible ingress. It initiates a paid session with the Provider Gateway, which authorises the upstream Firehose connection. You are streaming data through the full payment loop.

**What you should see:**

```
[consumer-sidecar] session started
[provider-gateway] StartSession: consumer=0xe908... escrow_balance=10000 GRT
[provider-gateway] PaymentSession opened
[firehose] processing block {"block_number": 1}
[firehose] processing block {"block_number": 2}
...
[provider-gateway] rav_request issued at threshold
[consumer-sidecar] RAV signed and returned
[provider-gateway] RAV accepted and stored
```

---

## 4. Manual setup (alternative to reflex)

If you prefer to run each service in a separate terminal, or if reflex is not available, follow these steps in order.

### Terminal 1: Docker + database migrations + devenv

```bash
# Start PostgreSQL, Redis, and pgweb
docker compose up -d

# Wait for PostgreSQL to be healthy, then apply migrations
./devel/migrate.sh up

# Start Anvil + deploy contracts + fund demo escrow
./devel/sds devenv
```

Leave this terminal running. The devenv process holds the Anvil chain; killing it loses all chain state.

**Devenv output to confirm success:**

```
Contracts deployed successfully.
Demo state prepared — escrow funded.
RPC available at http://localhost:58545
```

### Terminal 2: Consumer Sidecar

```bash
DLOG=".*=debug" ./devel/sds consumer sidecar \
  --grpc-listen-addr=:9002 \
  --config=devel/consumer-sidecar.direct.yaml \
  --plaintext \
  --signer-private-key=0x0bba7d355d1750fce9756af7887e826e8071a56d9d8e327f546b1f34c78f9281 \
  --collector-address=0x1d01649b4f94722b55b5c3b3e10fe26cd90c1ba9
```

Wait for: `Consumer Sidecar listening on :9002`

### Terminal 3: Provider Gateway

```bash
DLOG=".*=debug" ./devel/sds provider gateway \
  --repository-dsn="psql://dev-node:changeme@localhost:5432/dev-node?sslmode=disable" \
  --grpc-listen-addr=:9001 \
  --plugin-listen-addr=:9003 \
  --plaintext \
  --service-provider=0xa6f1845e54b1d6a95319251f1ca775b4ad406cdf \
  --collector-address=0x1d01649b4f94722b55b5c3b3e10fe26cd90c1ba9 \
  --escrow-address=0xfc7487a37ca8eac2e64cba61277aa109e9b8631e \
  --rpc-endpoint=http://localhost:58545 \
  --data-plane-endpoint="https://localhost:10016?insecure=true"
```

Wait for: `Provider Gateway listening on :9001`

> **In-memory mode (no Docker needed):** Replace `--repository-dsn="psql://..."` with `--repository-dsn="inmemory://"`. State is lost on restart.

### Terminal 4: Firehose Core

```bash
firecore -c devel/firecore.config.yaml -d ./devel/.firehose start
```

Wait for: `Substreams Tier1 listening on :10016`

### Terminal 5: Stream data

```bash
substreams run common@v0.1.0 map_clocks \
  -e localhost:9002 \
  --plaintext \
  -s 0 \
  -t +20
```

---

## Demo accounts and contract addresses

All addresses and keys in the dev environment are **deterministic** — the same every time `sds devenv` runs. Do not use these in production.

### Contract addresses

| Contract | Address |
|----------|---------|
| GraphTallyCollector | `0x1d01649b4f94722b55b5c3b3e10fe26cd90c1ba9` |
| PaymentsEscrow | `0xfc7487a37ca8eac2e64cba61277aa109e9b8631e` |
| SubstreamsDataService | `0x37478fd2f5845e3664fe4155d74c00e1a4e7a5e2` |

### Test accounts

Each account starts with 10 ETH and 10,000 GRT on the local chain.

| Role | Address | Private Key |
|------|---------|-------------|
| Service Provider | `0xa6f1845e54b1d6a95319251f1ca775b4ad406cdf` | `0x41942233cf1d78b6e3262f1806f8da36aafa24a941031aad8e056a1d34640f8d` |
| Payer | `0xe90874856c339d5d3733c92ea5acadc6014b34d5` | `0xe4c2694501255921b6588519cfd36d4e86ddc4ce19ab1bc91d9c58057c040304` |
| User1 | `0x90353af8461a969e755ef1e1dbadb9415ae5cb6e` | `0xdd02564c0e9836fb570322be23f8355761d4d04ebccdc53f4f53325227680a9f` |
| User2 | `0x9585430b90248cd82cb71d5098ac3f747f89793b` | `0xbc3def46fab7929038dfb0df7e0168cba60d3384aceabf85e23e5e0ff90c8fe3` |
| User3 | `0x37305c711d52007a2bcfb33b37015f1d0e9ab339` | `0x7acd0f26d5be968f73ca8f2198fa52cc595650f8d5819ee9122fe90329847c48` |
| Demo Signer | `0x82b6f0bbbab50f0ddc249e5ff60c6dc64d55340e` | `0x0bba7d355d1750fce9756af7887e826e8071a56d9d8e327f546b1f34c78f9281` |

---

## Oracle mode (alternative reflex config)

The default `.reflex` config runs in **direct mode** — the consumer sidecar connects directly to the provider gateway using config from `devel/consumer-sidecar.direct.yaml`.

An **oracle mode** config is also available. In oracle mode, the consumer sidecar contacts the oracle first to discover available providers before initiating a session.

```bash
reflex -c .reflex.oracle
```

This adds:
- Oracle service on `:9004`
- Consumer sidecar uses `devel/consumer-sidecar.oracle.yaml`

When streaming in oracle mode, packages that do not declare `package.network` need an explicit network flag:

```bash
substreams run common@v0.1.0 map_clocks \
  -e localhost:9002 \
  --plaintext \
  --network mainnet \
  -s 0 \
  -t +20
```

> **Note:** The oracle implementation is partial (MVP-005). Oracle mode works for local dev but the full permissionless provider discovery flow is not yet complete.

---

## Database

pgweb provides a browser UI for the PostgreSQL database:

**URL:** [http://localhost:8081](http://localhost:8081)

Tables of interest:

| Table | What's in it |
|-------|-------------|
| `sessions` | All sessions — status, consumer, provider, timestamps |
| `workers` | Active worker assignments per session |
| `usage_events` | Raw metered usage events from the plugin gateway |
| `quota_usage` | Per-session quota consumption |
| `ravs` | Accepted RAVs — the collectible payment records |

---

## Database migrations

```bash
./devel/migrate.sh up            # Apply all pending migrations
./devel/migrate.sh down          # Roll back one migration
./devel/migrate.sh version       # Show current schema version
./devel/migrate.sh new <name>    # Create a new migration file pair
```

---

## Running tests

Integration tests use Docker (real chain, real contracts). Make sure Docker is running:

```bash
go test ./test/integration/... -v
```

Unit tests (no Docker needed):

```bash
go test ./...
```

For the full Firehose Core runtime integration test (`TestFirecore`), see [Provider Runtime Compatibility](provider-runtime-compatibility.md).

---

## Troubleshooting

**`firecore` not found**

Make sure `firecore` is installed and on your `$PATH`. See the [firecore repository](https://github.com/streamingfast/firehose-core) for installation instructions.

**`dummy-blockchain` not found**

```bash
go install github.com/streamingfast/dummy-blockchain@latest
```

Ensure `$(go env GOPATH)/bin` is in your `$PATH`. In Firehose Core logs, look for:
```
auth plugin instantiation {"plugin_kind": "sds"}
processing block {"block_number": ...}
```

**Port already in use**

Check which process is using the port:

```bash
lsof -i :<port>
```

Kill the process or change the `--grpc-listen-addr` / `--plugin-listen-addr` flag on the relevant service.

**`migrate.sh up` fails with "already up to date"**

This is fine — it means migrations have already been applied. The reflex config suppresses this error automatically.

**Consumer sidecar shows `NeedMoreFunds` immediately**

The demo escrow was not funded. Restart `sds devenv` — it re-deploys and re-funds the escrow on each run.

**Firehose Core exits immediately**

The Plugin Gateway (Provider Gateway side, `:9003`) must be running before Firehose Core starts. In manual mode, make sure Terminal 3 is up before starting Terminal 4. In logs, look for:
```
errors about unknown `sds` plugin kind/scheme → your firecore binary is too old
```

**Oracle mode: `oracle-backed ingress requires a v3/v4 Substreams request`**

Your `substreams` CLI is too old. Upgrade to a version that supports v3/v4 package context.

**Oracle mode: `either <substreams_package.network> or <requested_network> is required`**

Your package does not embed a network. Pass `--network mainnet` (or the appropriate network) to the `substreams` CLI.

**pgweb shows empty tables**

No sessions have been created yet. Run a `substreams` command against `:9002` to initiate a paid stream and watch the tables populate.
