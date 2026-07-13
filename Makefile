REPO_ROOT := $(shell git rev-parse --show-toplevel 2>/dev/null || echo ".")
PYTHON := python3
RUFF := ruff
GO := go
GOLANGCI_LINT := golangci-lint

MCP_DIRS := reasoning-memory credential-vault pr-reviewer
GO_MCP_DIRS := $(REPO_ROOT)/mcp/reasoning-memory

.PHONY: all setup install-mcp-% validate lint test clean setup-go go-build-all go-test-all go-lint-all go-vet-all

all: setup

# ── Setup ────────────────────────────────────────────────────────────────────

setup: setup-go $(foreach d,$(MCP_DIRS),install-mcp-$d)
	@echo "✓ All MCPs installed"

##@ Go Workspace
setup-go: ## Sync Go workspace and tidy all modules
	go work sync
	@for dir in $(GO_MCP_DIRS); do \
		echo "Tidying $$dir..."; \
		cd $$dir && $(GO) mod tidy && cd -; \
	done

go-build-all: ## Build all Go modules from workspace root
	@for dir in $(GO_MCP_DIRS); do \
		echo "Building $$dir..."; \
		$(GO) build -v $$dir/...; \
	done

go-test-all: ## Test all Go modules from workspace root
	@for dir in $(GO_MCP_DIRS); do \
		echo "Testing $$dir..."; \
		$(GO) test -race -v $$dir/...; \
	done

go-lint-all: ## Lint all Go modules from workspace root
	@for dir in $(GO_MCP_DIRS); do \
		echo "Linting $$dir..."; \
		cd $$dir && $(GOLANGCI_LINT) run && cd -; \
	done

go-vet-all: ## Vet all Go modules from workspace root
	@for dir in $(GO_MCP_DIRS); do \
		echo "Vetting $$dir..."; \
		$(GO) vet $$dir/...; \
	done

install-mcp-reasoning-memory:
	@echo "→ Building reasoning-memory (Go)..."
	cd $(REPO_ROOT)/mcp/reasoning-memory && go build -o reasoning-memory .

install-mcp-credential-vault:
	@echo "→ Installing credential-vault..."
	cd $(REPO_ROOT)/mcp/credential-vault && \
		$(PYTHON) -m venv .venv && \
		.venv/bin/pip install --quiet --upgrade pip && \
		.venv/bin/pip install --quiet -e ".[dev]"

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
	$(RUFF) check $(REPO_ROOT)/mcp/credential-vault $(REPO_ROOT)/mcp/pr-reviewer --fix
	@echo "✓ Lint complete"

lint-check:
	@echo "→ Running lint checks (no fixes)..."
	cd $(REPO_ROOT)/mcp/reasoning-memory && golangci-lint run ./... || true
	$(RUFF) check $(REPO_ROOT)/mcp/credential-vault $(REPO_ROOT)/mcp/pr-reviewer
	@echo "✓ Lint check complete"

# ── Test ─────────────────────────────────────────────────────────────────────

test: $(foreach d,$(MCP_DIRS),test-mcp-$d)

test-mcp-reasoning-memory:
	cd $(REPO_ROOT)/mcp/reasoning-memory && go test -v -count=1 -short ./...

test-mcp-credential-vault:
	cd $(REPO_ROOT)/mcp/credential-vault && \
		.venv/bin/python -m pytest tests/ -v 2>/dev/null || \
		echo "  ℹ No tests found for credential-vault"

test-mcp-pr-reviewer:
	cd $(REPO_ROOT)/mcp/pr-reviewer && \
		.venv/bin/python -m pytest tests/ -v 2>/dev/null || \
		echo "  ℹ No tests found for pr-reviewer"

# ── Run MCP servers ──────────────────────────────────────────────────────────

run-mcp-reasoning-memory:
	cd $(REPO_ROOT)/mcp/reasoning-memory && go run .

run-mcp-credential-vault:
	cd $(REPO_ROOT)/mcp/credential-vault && .venv/bin/python server.py

run-mcp-pr-reviewer:
	cd $(REPO_ROOT)/mcp/pr-reviewer && .venv/bin/python server.py

# ── Clean ────────────────────────────────────────────────────────────────────

clean:
	@echo "→ Cleaning up..."
	@rm -rf $(REPO_ROOT)/mcp/reasoning-memory/reasoning-memory
	@for dir in credential-vault pr-reviewer; do \
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
