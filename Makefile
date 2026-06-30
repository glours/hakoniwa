# Makefile — hakoniwa
#
# Usage:
#   make              # same as make all
#   make build        # compile hako binary
#   make test         # run all tests
#   make lint         # run linter
#   make fmt          # format source
#   make clean        # remove build artefacts
#   make run ARGS="up -f path/to/hakoniwa.yaml"
#   make help         # show this help

# ─── Variables ────────────────────────────────────────────────────────────────

BINARY   := hako
GO       := go
GOFLAGS  :=
CMD_DIR  := ./cmd/hako

# Output directory for built binaries.
BIN_DIR  := bin

# ─── Default target ───────────────────────────────────────────────────────────

.DEFAULT_GOAL := all

# ─── Targets ──────────────────────────────────────────────────────────────────

.PHONY: all build test lint fmt clean run help

all: fmt build test lint ## Format, build, test, and lint (default target)

build: ## Compile the hako binary into ./bin/hako
	@mkdir -p $(BIN_DIR)
	$(GO) build $(GOFLAGS) -o $(BIN_DIR)/$(BINARY) $(CMD_DIR)

test: ## Run all tests (go test ./...)
	$(GO) test $(GOFLAGS) ./...

lint: ## Run linter: golangci-lint if available, else go vet + gofmt -l
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not found, falling back to go vet + gofmt"; \
		$(GO) vet ./...; \
		out=$$(gofmt -l .); \
		if [ -n "$$out" ]; then \
			echo "gofmt: the following files need formatting:"; \
			echo "$$out"; \
			exit 1; \
		fi; \
	fi

fmt: ## Format all Go source files (gofmt -w .)
	gofmt -w .

clean: ## Remove build artefacts (bin/)
	@rm -rf $(BIN_DIR)
	@echo "Cleaned $(BIN_DIR)/"

run: build ## Build and run hako (pass extra args with ARGS="up -f ...")
	./$(BIN_DIR)/$(BINARY) $(ARGS)

# ─── Help ─────────────────────────────────────────────────────────────────────

help: ## Show available targets and their descriptions
	@echo ""
	@echo "  hakoniwa Makefile — available targets:"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
	@echo ""
