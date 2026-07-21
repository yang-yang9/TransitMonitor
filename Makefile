# Build/test helpers. Allow overriding the go binary.
GO ?= $(shell which go 2>/dev/null || echo $(HOME)/.local/go/bin/go)
PKG := ./...
BIN := transitmonitor
PKG_MAIN := ./cmd/transitmonitor
TAG ?= dev

.PHONY: all build run test test-race vet fmt fmt-check tidy e2e selftest \
        docker-build docker-selftest docker-run docker-up docker-down docker-multiarch clean

all: build

build:
	$(GO) build -o $(BIN) $(PKG_MAIN)

run:
	$(GO) run $(PKG_MAIN)

test:
	$(GO) test $(PKG)

test-race:
	$(GO) test -race $(PKG)

vet:
	$(GO) vet $(PKG)

fmt:
	$(GO) fmt $(PKG)

fmt-check:
	@out=$$($(GO) fmt $(PKG)); if [ -n "$$out" ]; then echo "files need gofmt:"; echo "$$out"; exit 1; fi

tidy:
	$(GO) mod tidy

e2e:
	$(GO) test ./internal/e2e -run TestRun -v

selftest: build
	./transitmonitor -selftest

# --- Docker (requires docker / buildx on the host) ---

docker-build:
	docker build -t transitmonitor:$(TAG) .

docker-selftest:
	docker build -t transitmonitor:$(TAG) . >/dev/null && docker run --rm transitmonitor:$(TAG) -selftest

docker-run:
	docker run --rm -p 7421:7421 \
	  -v $(CURDIR)/config.yaml:/config/config.yaml:ro \
	  -v transitmonitor-data:/data \
	  -e TRANSMONITOR_DASHBOARD_TOKEN=$(TM_TOKEN) \
	  transitmonitor:$(TAG)

docker-up:
	docker compose up -d --build

docker-down:
	docker compose down

docker-multiarch:
	docker buildx build --platform linux/amd64,linux/arm64 -t transitmonitor:$(TAG) --push .

clean:
	rm -f $(BIN) $(BIN).test coverage.out
