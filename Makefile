VERSION ?= v0.4.2

.PHONY: test build release

test:
	go test ./...

build:
	go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o bin/nodexia ./cmd/nodexia

# Cut a release: bump every version reference, run tests, commit, and tag.
# Usage: make release VERSION=v0.2.1   (then push the branch and tag).
release:
	@bash scripts/release.sh "$(VERSION)"
