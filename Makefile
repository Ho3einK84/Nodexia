VERSION ?= v0.1.0

.PHONY: test build

test:
	go test ./...

build:
	go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/nodexia ./cmd/nodexia
