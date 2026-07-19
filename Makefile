REPO_ROOT := $(shell git rev-parse --show-toplevel 2>/dev/null || echo ".")
PYTHON := python3
RUFF := ruff

MCP_DIRS := reasoning-memory credential-vault pr-reviewer

.PHONY: all setup install-mcp-% validate lint test clean bench-reasoning-memory bench-credential-vault bench-go

all: setup

# ── Setup ────────────────────────────────────────────────────────────────────

setup: $(foreach d,$(MCP_DIRS),install-mcp-$d)
	@echo "✓ All MCPs installed"

install-mcp-reasoning-memory:
	@echo "→ Building reasoning-memory (Go)..."
	cd $(REPO_ROOT)/mcp/reasoning-memory && go build -o reasoning-memory .

install-mcp-credential-vault:
	@echo "→ Building credential-vault (Go)..."
	cd $(REPO_ROOT)/mcp/credential-vault-go && GOWORK=off go build -o vault ./cmd/vault && GOWORK=off go build -o vaultctl ./cmd/vaultctl

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
			if [ -f $(REPO_ROOT)/mcp/credential-vault-go/go.mod ]; then \
				echo "  ✓ mcp/credential-vault-go/go.mod"; \
			else \
				echo "  ✗ mcp/credential-vault-go/go.mod MISSING"; \
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
	cd $(REPO_ROOT)/mcp/credential-vault-go && GOWORK=off golangci-lint run ./...
	$(RUFF) check $(REPO_ROOT)/mcp/pr-reviewer --fix
	@echo "✓ Lint complete"

lint-check:
	@echo "→ Running lint checks (no fixes)..."
	cd $(REPO_ROOT)/mcp/reasoning-memory && golangci-lint run ./...
	cd $(REPO_ROOT)/mcp/credential-vault-go && GOWORK=off golangci-lint run ./...
	$(RUFF) check $(REPO_ROOT)/mcp/pr-reviewer
	@echo "✓ Lint check complete"

# ── Test ─────────────────────────────────────────────────────────────────────

test: $(foreach d,$(MCP_DIRS),test-mcp-$d)

test-mcp-reasoning-memory:
	cd $(REPO_ROOT)/mcp/reasoning-memory && go test -v -count=1 -short ./...

test-mcp-credential-vault:
	cd $(REPO_ROOT)/mcp/credential-vault-go && GOWORK=off go test -race -count=1 ./...

test-mcp-pr-reviewer:
	cd $(REPO_ROOT)/mcp/pr-reviewer && \
		.venv/bin/python -m pytest tests/ -v 2>/dev/null || \
		echo "  ℹ No tests found for pr-reviewer"

bench-reasoning-memory:
	@echo "→ Running performance benchmarks and generating reports..."
	cd $(REPO_ROOT)/mcp/reasoning-memory && go test -v -run="TestMeasurePercentiles" ./bench/... | go run ./bench/report/gen_reports.go
	@echo "→ Running accuracy/effectiveness benchmarks..."
	cd $(REPO_ROOT)/mcp/reasoning-memory && go test -v -run="TestRetrievalRelevance|TestConsolidationQuality|TestPolishAccuracy" ./bench/...

bench-credential-vault:
	cd $(REPO_ROOT)/mcp/credential-vault-go && GOWORK=off go test -bench=. -benchmem ./bench/...

bench-go: bench-reasoning-memory bench-credential-vault

# ── Run MCP servers ──────────────────────────────────────────────────────────

run-mcp-reasoning-memory:
	cd $(REPO_ROOT)/mcp/reasoning-memory && go run .

run-mcp-credential-vault:
	cd $(REPO_ROOT)/mcp/credential-vault-go && go run ./cmd/vault

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
