# Overview

The Substreams Data Service (SDS) is the payment infrastructure layer for paid Substreams data streaming on The Graph Network.

It implements the [TAP (Timeline Aggregation Protocol) V2](https://docs.thegraph.com/tap) payment flow on top of Horizon smart contracts — handling session initiation, streaming receipts, RAV (Receipt Aggregate Voucher) signing and aggregation, and on-chain settlement.

## What it is not

SDS is **not** a Substreams node. It does not serve blockchain data. The actual Substreams/Firehose data plane is handled by [Firehose Core](https://github.com/streamingfast/firehose-core). SDS components run **alongside** it, acting as the payment and metering layer.

## Two sides

SDS has two distinct sides — one for consumers, one for providers.

### Consumer side

The **consumer sidecar** runs alongside any Substreams client application. It:

- Initiates paid sessions with a provider
- Signs RAVs (payment receipts) as usage accumulates
- Monitors escrow balance and signals when funds are low
- Terminates sessions cleanly

### Provider side

The **provider stack** runs alongside a Firehose/Substreams node. It consists of three components that work together:

- **Provider Gateway** — public-facing payment control plane; validates sessions, tracks usage, requests RAVs
- **Plugin Gateway** — internal Firehose plugin; handles auth, session propagation, and usage metering inside Firehose Core
- **Repository** — durable persistence for sessions, usage events, and accepted RAVs (in-memory for dev, PostgreSQL for production)

There is also an **Oracle** service for provider discovery — partially implemented, available in the oracle reflex configuration.

## Where it fits in The Graph ecosystem

```
Substreams Client
    └── Consumer Sidecar (SDS)          ← payment management
            └── Provider Gateway (SDS)   ← payment validation
                    └── Plugin Gateway (SDS)  ← Firehose auth/session/metering
                            └── Firehose Core ← actual blockchain data
```

The Graph Network's escrow contracts (Horizon) sit on-chain. Funds flow from consumer to provider via RAV aggregation and on-chain `collect()` calls — SDS coordinates this without touching the data path directly.

## Payment flow summary

1. Consumer sidecar calls `StartSession` on the provider gateway
2. Provider validates the consumer and checks escrow balance
3. Data streams through Firehose — the plugin gateway meters usage and forwards billing events
4. As usage accumulates, the provider requests RAVs from the consumer sidecar at deterministic thresholds
5. The consumer sidecar signs RAVs and sends them back; the provider stores them
6. If funds run low, the provider sends a `NeedMoreFunds` signal; the session terminates
7. The provider operator later collects accepted RAVs on-chain via the settlement CLI (post-MVP)

## Current state (MVP)

The core payment loop is implemented and working end-to-end in the development environment:

- Horizon V2 / TAP signing, verification, and RAV aggregation
- Consumer sidecar (InitSession, ReportUsage, EndSession)
- Provider gateway (StartSession, PaymentSession, SubmitRAV)
- Provider plugins integrated with Firehose Core
- Low-funds detection and session termination
- Deterministic RAV request thresholds
- Local devenv (Anvil + contract deployment)
- PostgreSQL and in-memory repository backends

Work still in progress (post-Juan OOO):

- Collection CLI — on-chain RAV settlement
- Funding CLI — consumer escrow top-up workflows
- Consumer-facing Substreams endpoint/proxy (so clients connect to the sidecar directly)
- Oracle — full provider discovery flow
- Authenticated operator surfaces
- Observability hardening (metrics)

For a detailed status breakdown, see [`plans/mvp-gap-analysis.md`](../plans/mvp-gap-analysis.md).
