BIN     := ishakat
PKG     := ./cmd/ishakat
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test check android clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN) $(PKG)

test:
	go test ./...

check: test
	go vet ./...
	OMNIROUTE_API_KEY=$${OMNIROUTE_API_KEY:-check-key} ./bin/$(BIN) config check --strict

android:
	CGO_ENABLED=1 GOOS=android GOARCH=arm64 \
	CC=$(NDK)/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android24-clang \
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN)-android-arm64 $(PKG)

clean:
	rm -rf bin/
