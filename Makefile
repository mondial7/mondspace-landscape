.PHONY: help test test-js vet fmt fmt-check build install run ui screenshots release-snapshot release clean

BINARY := landscape
PKG    := ./...
MAIN   := ./cmd/landscape

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## /{printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

test: test-js ## Run unit tests with race detector + coverage (Go and UI)
	go test $(PKG) -race -coverprofile=coverage.out

test-js: ## Run the UI's JavaScript unit tests (needs Node 20+)
	node --test 'internal/web/jstest/*.test.mjs'

ui: install ## Analyze this repo, serving the UI from disk (reload to see UI edits)
	LANDSCAPE_WEB_DIR=$(CURDIR)/internal/web/static landscape .

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
	goreleaser release --snapshot --clean --skip=sbom

release: ## Tag and push VERSION (make release VERSION=v0.2.0); the release workflow publishes it
	@test -n "$(VERSION)" || { echo "usage: make release VERSION=vX.Y.Z"; exit 1; }
	@grep -qF "## [$(patsubst v%,%,$(VERSION))]" CHANGELOG.md || { echo "CHANGELOG.md has no section for $(VERSION)"; exit 1; }
	git tag -a $(VERSION) -m "$(VERSION)"
	git push origin $(VERSION)

clean: ## Remove build artefacts
	rm -rf $(BINARY) dist coverage.out
