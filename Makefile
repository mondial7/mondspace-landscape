.PHONY: help test vet fmt fmt-check build install run screenshots release-snapshot release clean

BINARY := landscape
PKG    := ./...
MAIN   := ./cmd/landscape

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## /{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

test: ## Run unit tests with race detector + coverage
	go test $(PKG) -race -coverprofile=coverage.out

vet: ## go vet
	go vet $(PKG)

fmt: ## Apply gofmt -s in place
	gofmt -s -w .

fmt-check: ## Fail if gofmt would change anything
	@unformatted=$$(gofmt -s -l .); \
	if [ -n "$$unformatted" ]; then echo "Not gofmt'd:"; echo "$$unformatted"; exit 1; fi

build: ## Build the binary at the repo root
	go build -trimpath -o $(BINARY) $(MAIN)

install: ## go install into $$GOBIN
	go install $(MAIN)

run: ## Analyze this repository and open the UI
	go run $(MAIN) .

screenshots: install ## Regenerate docs/images with the tool itself (needs Chrome)
	scripts/screenshots.sh . docs/images

release-snapshot: ## Build release archives locally without publishing
	goreleaser release --snapshot --clean

release: ## Publish a release for the current tag (GitHub release + Homebrew tap)
	GITHUB_TOKEN=$$(gh auth token) HOMEBREW_TAP_TOKEN=$$(gh auth token) goreleaser release --clean

clean: ## Remove build artefacts
	rm -rf $(BINARY) dist coverage.out
