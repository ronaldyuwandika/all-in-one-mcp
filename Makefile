REPO_ROOT := $(shell git rev-parse --show-toplevel 2>/dev/null || echo ".")
PYTHON := python3
RUFF := ruff

MCP_DIRS := reasoning-memory credential-vault pr-reviewer

.PHONY: all setup build install install-mcp-% validate lint lint-all lint-check test test-all test-reasoning-memory test-credential-vault test-pr-reviewer test-secretdetect clean distclean bench-reasoning-memory bench-credential-vault bench-go bench-all run-mcp-reasoning-memory run-mcp-credential-vault run-mcp-pr-reviewer

all: setup

# ── Build & Install (Go binaries) ────────────────────────────────────────────

BIN_DIR := $(REPO_ROOT)/bin
REASONING_MEMORY_BIN := $(BIN_DIR)/reasoning-memory
INSTALL_BIN_DIR := $(HOME)/mcp/bin

build: $(REASONING_MEMORY_BIN)

$(REASONING_MEMORY_BIN):
	@echo "→ Building reasoning-memory..."
	@mkdir -p $(BIN_DIR)
	cd $(REPO_ROOT)/mcp/reasoning-memory && go build -o $(REASONING_MEMORY_BIN) .

install: build
	@echo "→ Installing reasoning-memory to $(INSTALL_BIN_DIR)..."
	@mkdir -p $(INSTALL_BIN_DIR)
	install -m 0755 $(REASONING_MEMORY_BIN) $(INSTALL_BIN_DIR)/reasoning-memory
	@echo "✓ Installed: $(INSTALL_BIN_DIR)/reasoning-memory"

# ── Setup ────────────────────────────────────────────────────────────────────

setup: $(foreach d,$(MCP_DIRS),install-mcp-$d)
	@echo "✓ All MCPs installed"

install-mcp-reasoning-memory:
	@echo "→ Installing reasoning-memory (Go)..."
	@mkdir -p $(INSTALL_BIN_DIR)
	cd $(REPO_ROOT)/mcp/reasoning-memory && go build -o $(INSTALL_BIN_DIR)/reasoning-memory .
	@echo "✓ Installed: $(INSTALL_BIN_DIR)/reasoning-memory"

install-mcp-credential-vault:
	@echo "→ Installing credential-vault (Go)..."
	@mkdir -p $(INSTALL_BIN_DIR)
	cd $(REPO_ROOT)/mcp/credential-vault && GOWORK=off go build -o $(INSTALL_BIN_DIR)/vault ./cmd/vault && GOWORK=off go build -o $(INSTALL_BIN_DIR)/vaultctl ./cmd/vaultctl
	@echo "✓ Installed: $(INSTALL_BIN_DIR)/vault"
	@echo "✓ Installed: $(INSTALL_BIN_DIR)/vaultctl"

install-mcp-pr-reviewer:
	@echo "→ Installing pr-reviewer..."
	cd $(REPO_ROOT)/mcp/pr-reviewer && \
		$(PYTHON) -m venv .venv && \
		.venv/bin/pip install --quiet --upgrade pip && \
		.venv/bin/pip install --quiet -e ".[dev]"

# ── Validate ─────────────────────────────────────────────────────────────────

validate:
	@echo "→ Validating MCP configurations..."
	@for dir in $(MCP_DIRS); do \
		if [ $$dir = "reasoning-memory" ]; then \
			if [ -f $(REPO_ROOT)/mcp/$$dir/go.mod ]; then \
				echo "  ✓ mcp/$$dir/go.mod"; \
			else \
				echo "  ✗ mcp/$$dir/go.mod MISSING"; \
			fi; \
			if [ -f $(REPO_ROOT)/mcp/$$dir/main.go ]; then \
				echo "  ✓ mcp/$$dir/main.go"; \
			fi; \
		elif [ $$dir = "credential-vault" ]; then \
			if [ -f $(REPO_ROOT)/mcp/credential-vault/go.mod ]; then \
				echo "  ✓ mcp/credential-vault/go.mod"; \
			else \
				echo "  ✗ mcp/credential-vault/go.mod MISSING"; \
			fi; \
		else \
			if [ -f $(REPO_ROOT)/mcp/$$dir/pyproject.toml ]; then \
				echo "  ✓ mcp/$$dir/pyproject.toml"; \
			else \
				echo "  ✗ mcp/$$dir/pyproject.toml MISSING"; \
			fi; \
			if [ -f $(REPO_ROOT)/mcp/$$dir/server.py ]; then \
				echo "  ✓ mcp/$$dir/server.py"; \
			fi; \
		fi; \
		if [ -f $(REPO_ROOT)/mcp/$$dir/AI_GUIDE.md ]; then \
			echo "  ✓ mcp/$$dir/AI_GUIDE.md"; \
		fi; \
	done
	@echo "✓ Validation complete"

# ── Lint ─────────────────────────────────────────────────────────────────────

lint:
	@echo "→ Running linters..."
	cd $(REPO_ROOT)/mcp/reasoning-memory && golangci-lint run ./... || true
	cd $(REPO_ROOT)/mcp/credential-vault && GOWORK=off golangci-lint run ./...
	$(RUFF) check $(REPO_ROOT)/mcp/pr-reviewer --fix
	@echo "✓ Lint complete"

lint-all: lint

lint-check:
	@echo "→ Running lint checks (no fixes)..."
	cd $(REPO_ROOT)/mcp/reasoning-memory && golangci-lint run ./...
	cd $(REPO_ROOT)/mcp/credential-vault && GOWORK=off golangci-lint run ./...
	$(RUFF) check $(REPO_ROOT)/mcp/pr-reviewer
	@echo "✓ Lint check complete"

# ── Test ─────────────────────────────────────────────────────────────────────

.PHONY: test test-all test-reasoning-memory test-credential-vault test-pr-reviewer test-secretdetect

test: test-reasoning-memory test-credential-vault test-pr-reviewer test-secretdetect
test-all: test

test-reasoning-memory:
	@echo "→ Running reasoning-memory tests..."
	$(MAKE) -C $(REPO_ROOT)/mcp/reasoning-memory test

test-mcp-reasoning-memory: test-reasoning-memory

test-credential-vault:
	@echo "→ Running credential-vault tests..."
	cd $(REPO_ROOT)/mcp/credential-vault && GOWORK=off go test -race -count=1 ./...

test-mcp-credential-vault: test-credential-vault

test-pr-reviewer:
	@echo "→ Running pr-reviewer tests..."
	@test -x $(REPO_ROOT)/mcp/pr-reviewer/.venv/bin/python || \
		(echo "✗ pr-reviewer virtualenv missing; run 'make setup' first" && exit 1)
	cd $(REPO_ROOT)/mcp/pr-reviewer && .venv/bin/python -m pytest tests/ -v

test-mcp-pr-reviewer: test-pr-reviewer

test-secretdetect:
	@echo "→ Running secretdetect tests..."
	cd $(REPO_ROOT)/pkg/secretdetect && go test -v -count=1 ./...

bench-reasoning-memory:
	@echo "→ Running performance benchmarks and generating reports..."
	$(MAKE) -C $(REPO_ROOT)/mcp/reasoning-memory bench

bench-credential-vault:
	cd $(REPO_ROOT)/mcp/credential-vault && GOWORK=off go test -bench=. -benchmem ./bench/...

bench-go: bench-reasoning-memory bench-credential-vault

bench-all: bench-go

# ── Run MCP servers ──────────────────────────────────────────────────────────

run-mcp-reasoning-memory:
	cd $(REPO_ROOT)/mcp/reasoning-memory && go run .

run-mcp-credential-vault:
	cd $(REPO_ROOT)/mcp/credential-vault && go run ./cmd/vault

run-mcp-pr-reviewer:
	cd $(REPO_ROOT)/mcp/pr-reviewer && .venv/bin/python server.py

# ── Clean ────────────────────────────────────────────────────────────────────

clean:
	@echo "→ Cleaning up..."
	@rm -rf $(BIN_DIR)
	@rm -rf $(REPO_ROOT)/mcp/reasoning-memory/reasoning-memory
	@for dir in pr-reviewer; do \
		rm -rf $(REPO_ROOT)/mcp/$$dir/.venv; \
		rm -rf $(REPO_ROOT)/mcp/$$dir/__pycache__; \
		find $(REPO_ROOT)/mcp/$$dir -name '*.pyc' -delete; \
		find $(REPO_ROOT)/mcp/$$dir -name '__pycache__' -type d -exec rm -rf {} + 2>/dev/null || true; \
	 done

	@echo "✓ Clean complete"

distclean: clean
	@echo "→ Removing all state..."
	rm -rf $(REPO_ROOT)/*.egg-info
	rm -rf $(REPO_ROOT)/dist
	rm -rf $(REPO_ROOT)/build
	@echo "✓ Distclean complete"
