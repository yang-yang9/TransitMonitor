# syntax=docker/dockerfile:1
# Multi-stage build. modernc.org/sqlite is pure-Go (no CGO), so the binary is
# fully static → clean cross-arch (amd64/arm64) builds.

FROM golang:1.25-alpine AS build
WORKDIR /src
# Cache deps first.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/transitmonitor ./cmd/transitmonitor

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget && update-ca-certificates
WORKDIR /app
COPY --from=build /out/transitmonitor /app/transitmonitor
# Sensible Docker defaults; override via env or compose.
ENV TRANSMONITOR_CONFIG=/config/config.yaml \
    TRANSMONITOR_DB_PATH=/data/transitmonitor.db \
    TRANSMONITOR_DASHBOARD_ADDR=0.0.0.0:7421 \
    TRANSMONITOR_LOG_LEVEL=info
VOLUME ["/data"]
EXPOSE 7421
# wget available in the image → native healthcheck.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:7421/healthz || exit 1
ENTRYPOINT ["/app/transitmonitor"]
# No args → serve (reads TRANSMONITOR_CONFIG). `docker run ... -selftest` works too.
