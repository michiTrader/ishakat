BIN     := ishakat
PKG     := ./cmd/ishakat
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# EXE is the extension the host needs on an executable. On Windows a file
# without ".exe" is not a program to the loader at all: PowerShell falls back to
# its file associations and opens the binary in whatever is registered for
# unknown types, which is why `./ishakat` came up in an editor while
# `go run ./cmd/ishakat` worked. The name is the whole fix.
#
# It is detected rather than asked for, because the person who hits this is
# building on Windows and typing `make build` like everyone else. MSYS2 and Git
# Bash report MINGW*/MSYS* in uname; native cmd/PowerShell have no uname at all,
# and there OS=Windows_NT is always set.
ifeq ($(OS),Windows_NT)
EXE := .exe
else
UNAME_S := $(shell uname -s 2>/dev/null)
ifneq (,$(findstring MINGW,$(UNAME_S))$(findstring MSYS,$(UNAME_S))$(findstring CYGWIN,$(UNAME_S)))
EXE := .exe
else
EXE :=
endif
endif

.PHONY: build test race fmt check android windows windows-arm64 clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN)$(EXE) $(PKG)

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
	OMNIROUTE_API_KEY=$${OMNIROUTE_API_KEY:-check-key} ./bin/$(BIN)$(EXE) config check --strict

android:
	CGO_ENABLED=1 GOOS=android GOARCH=arm64 \
	CC=$(NDK)/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android24-clang \
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN)-android-arm64 $(PKG)

# Cross-compiled Windows builds. The suffix is hardcoded here instead of using
# $(EXE): the target's output runs on Windows regardless of what is building it,
# and a cross-build from Linux that produced an extensionless file would
# reproduce the original bug on the machine it was built for.
windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN)-windows-amd64.exe $(PKG)

windows-arm64:
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 \
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN)-windows-arm64.exe $(PKG)

clean:
	rm -rf bin/
