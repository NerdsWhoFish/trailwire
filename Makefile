BIN := $(HOME)/bin/trailwire
PKG := ./cmd/trailwire

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the CLI
	go build -trimpath -o trailwire $(PKG)

.PHONY: test
test: ## Run the race-enabled test suite
	go test -race -shuffle=on ./...

.PHONY: check
check: ## Verify formatting, vet, tests, and the release build
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	go vet ./...
	go test -race -shuffle=on ./...
	goreleaser release --snapshot --clean --skip=publish

.PHONY: install
install: test ## Install into ~/bin
	mkdir -p $(dir $(BIN))
	go build -trimpath -o $(BIN) $(PKG)
	@echo "installed $(BIN)"
