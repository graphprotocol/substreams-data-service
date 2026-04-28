# Architecture

## Components

### Consumer Sidecar (`consumer/sidecar/`)

Runs alongside a Substreams client. Exposes a gRPC interface the client uses to manage sessions and report usage.

**Key RPCs:**

| RPC | What it does |
|-----|-------------|
| `InitSession` | Creates a session, resolves payer/signer, validates the collector |
| `ReportUsage` | Aggregates usage, creates RAVs, sends to provider; detects `NeedMoreFunds` |
| `EndSession` | Terminates the session and cleans up |

`payment_session_manager.go` manages the bidirectional stream to the Provider Gateway.

> **MVP note:** The consumer-facing Substreams-compatible proxy endpoint (MVP-007, MVP-017) is not yet implemented. Clients currently integrate via the `sds_sink` wrapper.

---

### Provider Gateway (`provider/gateway/`)

The public-facing payment control plane. Consumers connect here to initiate sessions and stream payment updates.

**Key RPCs:**

| RPC | What it does |
|-----|-------------|
| `StartSession` | Initialises session, validates consumer, checks escrow balance |
| `PaymentSession` | Bidirectional stream: handles usage, tracks funds, requests RAVs at thresholds, enforces low-funds termination |
| `SubmitRAV` | Validates and stores a RAV with EIP-712 signature verification |
| `GetSessionStatus` | Operator read surface for session inspection |

Low-funds handling: the provider detects when the escrow balance is insufficient mid-stream and sends `NeedMoreFunds` to the consumer sidecar, which propagates this to the client.

---

### Provider Plugin Gateway (`provider/plugin/`)

Registers auth, session, and usage-metering plugins with Firehose Core via the `BorrowWorkerRequest` / `Event` interfaces. Always runs colocated with the Provider Gateway — they communicate via local in-process notification, not gRPC.

**Three plugin services:**

| Service | Interface | What it does |
|---------|-----------|-------------|
| Auth | `BorrowWorkerRequest` | Validates session credentials on each Firehose request |
| Session | `BorrowWorkerRequest` | Propagates session ID, applies per-session quotas |
| Usage (Metering) | `Event` | Collects byte-level usage data and forwards to billing |

---

### Oracle (`cmd/sds/oracle serve`)

Provider discovery service. Returns eligible providers and pricing recommendations to consumer sidecars. Currently backed by a manually curated whitelist (not permissionless). Available in the oracle reflex configuration.

> **Status:** Partially implemented. The command is runnable but the full provider discovery flow is in progress (MVP-005).

---

### Horizon (`horizon/`)

The cryptographic core. Implements EIP-712 typed data signing and verification for RAVs and Receipts.

Key types: `Receipt`, `RAV`, signature domain.

`aggregator.go` merges receipts into RAVs with strict validation (collectionId, payer, receiver uniqueness). The `devenv/` subdirectory handles local Anvil chain setup and Horizon/TAP contract deployment for integration tests.

---

### Repository (`provider/repository/`)

Pluggable persistence layer with two backends:

| Backend | When to use |
|---------|------------|
| `inmemory.go` | Development and demo (no DSN needed, state lost on restart) |
| `psql/` | Production-grade; PostgreSQL with golang-migrate schema management |

The backend is selected by the `--repository-dsn` flag: omit it for in-memory, provide a `psql://` DSN for PostgreSQL.

**Schema:** `sessions`, `workers`, `usage_events`, `quota_usage`, `ravs` tables.

See [`docs/provider-persistence-boundary.md`](provider-persistence-boundary.md) for the detailed boundary between runtime session state and collectible RAV state.

---

## Network topology

```
┌────────────────────────────────────────────────────────────────┐
│ Consumer host                                                   │
│                                                                 │
│   Substreams Client ──── Consumer Sidecar (:9002)              │
│                              │                                  │
└──────────────────────────────┼──────────────────────────────────┘
                               │ gRPC (plaintext in dev, TLS in prod)
┌──────────────────────────────┼──────────────────────────────────┐
│ Provider host                │                                  │
│                              ▼                                  │
│              Provider Gateway (:9001)                           │
│                      │   (local notification)                   │
│              Plugin Gateway (:9003) ◄──── Firehose Core        │
│                                               (:10016 Substreams│
│                                                :10015 Firehose) │
└────────────────────────────────────────────────────────────────┘
```

**Ports at a glance:**

| Port | Service |
|------|---------|
| 9001 | Provider Gateway (gRPC) |
| 9002 | Consumer Sidecar (gRPC) |
| 9003 | Plugin Gateway (internal — Firehose only) |
| 9004 | Oracle (oracle mode only) |
| 10015 | Firehose gRPC (TLS) |
| 10016 | Substreams Tier1 gRPC (TLS) |
| 5432 | PostgreSQL |
| 6379 | Redis |
| 8081 | pgweb (database UI) |
| 58545 | Local devenv RPC (Anvil) |

---

## Key design decisions

**Plugin Gateway ↔ Provider Gateway is local notification, not gRPC.**
These two components are always colocated. The separation exists for security boundary reasons (plugin-facing vs payment-facing), but there is no reason to split them across hosts. A post-MVP improvement task exists to make the separation cleaner, but it is deferred.

**Session-local funding.**
Funds are scoped per session, not globally across concurrent streams. If a consumer runs multiple concurrent streams, each session has its own budget. Aggregate exposure tracking across concurrent sessions is an explicit MVP non-goal.

**Pricing is provider-authoritative.**
The oracle provides pricing metadata, but the provider handshake response is the binding pricing authority.

**Two persistence backends.**
In-memory for testing and demos; PostgreSQL for durable production state. The in-memory backend loses all state on restart — acceptable for demos, not for production.

**TLS by default.**
The `--plaintext` flag exists for local development only. Production deployments always use TLS. Firehose Core uses a self-signed TLS cert on its gRPC ports (note the `*` suffix in `firecore.config.yaml`).

---

## Repo layout

```
cmd/sds/          CLI entrypoints (consumer sidecar, provider gateway, oracle, devenv, tools)
consumer/
  sidecar/        Consumer payment sidecar (session init, RAV signing, usage reporting)
provider/
  gateway/        Public-facing payment gateway (session management, RAV validation)
  auth/           Firehose plugin — authentication service
  session/        Firehose plugin — session management + quotas
  usage/          Firehose plugin — usage metering
  plugin/         Plugin gateway (local, not gRPC — registers above services with firecore)
  repository/     Persistence (in-memory or PostgreSQL)
sidecar/          Shared components (session types, escrow/collector queriers, pricing, transport)
horizon/          Core RAV/Receipt types, EIP-712 signing, signature verification
  devenv/         Local Anvil chain + contract deployment for testing
internal/         Operator auth utilities
proto/            Protobuf service definitions
pb/               Generated Go protobuf code
contracts/        Solidity ABIs + bytecode (embedded for test deployment)
devel/            Dev tooling (reflex configs, wrapper scripts, migrate.sh, firecore config)
docs/             Architecture and design docs (you are here)
plans/            MVP backlog and gap analysis
test/integration/ Integration tests (Docker-based, real chain)
```

---

## Key files for onboarding

| File | Why read it |
|------|-------------|
| `README.md` | Quick start, dev setup overview |
| `AGENTS.md` | Coding conventions, CLI patterns, domain types, concurrency rules |
| `docs/mvp-scope.md` | Stable target definition, non-goals, MVP assumptions |
| `plans/mvp-implementation-backlog.md` | Active task tracker with done/not_started/in_progress status |
| `plans/mvp-gap-analysis.md` | Current-state assessment — what's done, what's missing |
| `docs/provider-persistence-boundary.md` | What goes in the DB and why |
| `docs/operator-auth.md` | Operator authentication contract (roles, bearer tokens) |
