ARG GO_VERSION=1.25

FROM golang:${GO_VERSION}-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build \
      -trimpath \
      -buildvcs=false \
      -ldflags="-s -w -X github.com/graphprotocol/substreams-data-service.Version=${VERSION}" \
      -o /out/sds \
      ./cmd/sds

FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="Substreams Data Service"
LABEL org.opencontainers.image.description="Provider gateway for Substreams Data Service"
LABEL org.opencontainers.image.source="https://github.com/graphprotocol/substreams-data-service"

COPY --from=build /out/sds /usr/local/bin/sds

USER nonroot:nonroot
WORKDIR /

ENTRYPOINT ["/usr/local/bin/sds"]
CMD ["provider", "gateway", "--help"]
