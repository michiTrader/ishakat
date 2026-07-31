BIN     := ishakat
PKG     := ./cmd/ishakat
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test race fmt check android clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN) $(PKG)

test:
	go test ./...

# El parser de SSE y el canal de eventos son concurrentes: el detector de
# carreras es obligatorio antes de cerrar un paso, no opcional.
race:
	go test -race ./...

fmt:
	gofmt -w cmd internal

check: test
	go vet ./...
	@test -z "$$(gofmt -l cmd internal)" || { echo "✗ archivos sin formatear:"; gofmt -l cmd internal; exit 1; }
	OMNIROUTE_API_KEY=$${OMNIROUTE_API_KEY:-check-key} ./bin/$(BIN) config check --strict

android:
	CGO_ENABLED=1 GOOS=android GOARCH=arm64 \
	CC=$(NDK)/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android24-clang \
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN)-android-arm64 $(PKG)

clean:
	rm -rf bin/
