# devdns -- local development DNS resolver backed by CoreDNS.
BINARY  := devdns
BIN_DIR := bin
GO      ?= go

.DEFAULT_GOAL := help

.PHONY: help build install test vet fmt tidy coredns generate start stop restart reload status clean

help: ## Show this help
	@awk 'BEGIN{FS=":.*## "} /^[a-zA-Z_-]+:.*## /{printf "  \033[36m%-10s\033[0m %s\n",$$1,$$2}' $(MAKEFILE_LIST)

build: ## Build the devdns CLI into ./bin
	$(GO) build -o $(BIN_DIR)/$(BINARY) ./cmd/devdns

install: ## Install devdns with `go install`
	$(GO) install ./cmd/devdns

test: ## Run the test suite
	$(GO) test ./...

vet: ## Run go vet
	$(GO) vet ./...

fmt: ## Format the code
	$(GO) fmt ./...

tidy: ## Tidy go.mod / go.sum
	$(GO) mod tidy

coredns: ## Download the CoreDNS binary into ./bin
	./scripts/install-coredns.sh $(BIN_DIR)

generate: build ## Regenerate the Corefile and zone files from records.yaml
	$(BIN_DIR)/$(BINARY) generate

start: build ## Start CoreDNS
	$(BIN_DIR)/$(BINARY) start

stop: build ## Stop CoreDNS
	$(BIN_DIR)/$(BINARY) stop

restart: build ## Restart CoreDNS
	$(BIN_DIR)/$(BINARY) restart

reload: build ## Regenerate config and let a running CoreDNS reload
	$(BIN_DIR)/$(BINARY) reload

status: build ## Show CoreDNS status
	$(BIN_DIR)/$(BINARY) status

clean: ## Remove build artifacts and runtime state
	rm -rf $(BIN_DIR) .devdns
