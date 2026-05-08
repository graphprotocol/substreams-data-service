# Provider Deployment Base

This kustomize base deploys only the provider gateway and a PostgreSQL repository backed by the CloudNativePG `Cluster` CRD.

Redis/Dragonfly is intentionally not included. The provider gateway does not consume Redis today; its production repository backend is PostgreSQL via `--repository-dsn`.

## Required Before Apply

- CloudNativePG operator installed in the cluster.
- An overlay that replaces placeholder chain addresses, RPC endpoint, and image tag.

## Build Image

```bash
docker buildx build \
  --build-arg VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
  -t harbor.mgmt.infra.graphops.xyz/infra/sds-provider:latest \
  .
```

The image builds a static `sds` binary and runs it from `gcr.io/distroless/static-debian12:nonroot`.

## Apply

```bash
kubectl apply -k deploy/provider/base
```

The payment gateway listens on `sds-provider-payment:9001` using plaintext h2c. Terminate TLS at your ingress, gateway, or load balancer if this endpoint is exposed outside the cluster. The plugin gateway listens on `sds-provider-plugin:9003` and should only be reachable by internal Firehose/Substreams pods.

The provider deployment defaults to one replica. Do not scale it for HA until the provider's cross-pod session, quota, and RAV update paths have been reviewed and hardened for concurrent writers.

The PostgreSQL DSN is built from the CloudNativePG-generated `sds-postgres-app` secret. The deployment reads `username`, `password`, `host`, `port`, and `dbname`, then composes the provider's required `psql://` repository DSN via Kubernetes env var interpolation.

## Overlay Notes

- Replace `sds-provider-config` values for `CHAIN_ID`, `SERVICE_PROVIDER_ADDRESS`, `COLLECTOR_ADDRESS`, and `ESCROW_ADDRESS`.
- Replace `sds-provider-secret` value for `rpc-endpoint`.
