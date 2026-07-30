BIN     := ishakat
VERSION ?= 0.0.1-spike
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: run doctor build clean tidy

run:
	go run ./cmd/$(BIN)

doctor:
	go run ./cmd/$(BIN) doctor

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BIN) ./cmd/$(BIN)

tidy:
	go mod tidy

clean:
	rm -rf dist
