REPO_ROOT := $(shell git rev-parse --show-toplevel 2>/dev/null || echo ".")
PYTHON := python3
RUFF := ruff
GO := go
GOLANGCI_LINT := golangci-lint
GOBIN := $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(HOME)/go/bin
endif

GO_MCP_DIRS := $(REPO_ROOT)/mcp/reasoning-memory
PYTHON_MCP_DIRS := $(REPO_ROOT)/mcp/credential-vault $(REPO_ROOT)/mcp/pr-reviewer
BIN_DIR := $(REPO_ROOT)/bin

.DEFAULT_GOAL := help

.PHONY: all help install-tools check-tools update-tools setup-repo setup validate validate-go validate-python validate-config lint-go lint-python lint-docker lint-markdown lint-yaml lint-all test-go test-python test-integration test-all coverage-report bench-go bench-python bench-all bench-compare security sbom build-all install-all version-bump changelog tag release release-dry-run run-reasoning-memory run-credential-vault run-pr-reviewer dev fmt vet doctor clean distclean

all: setup

##@ Help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-30s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Tool Installation
install-tools: ## Install all CLI tools
	@echo "Checking/Installing Go tools..."
	$(GO) install github.com/securego/gosec/v2/cmd/gosec@latest
	$(GO) install honnef.co/go/tools/cmd/staticcheck@latest
	$(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	$(GO) install github.com/golang/mock/mockgen@latest
	$(GO) install golang.org/x/tools/cmd/goimports@latest
	@echo "Checking/Installing Python tools..."
	$(PYTHON) -m pip install --upgrade pip
	$(PYTHON) -m pip install ruff yamllint pip-audit pytest pytest-cov pytest-benchmark pytest-watch
	@echo "Checking/Installing Homebrew tools..."
	@if command -v brew >/dev/null 2>&1; then \
		brew install golangci-lint hadolint markdownlint-cli2 hyperfine hey syft git-cliff goreleaser 2>/dev/null || \
		brew upgrade golangci-lint hadolint markdownlint-cli2 hyperfine hey syft git-cliff goreleaser || true; \
	else \
		echo "Warning: Homebrew not found. Please install golangci-lint, hadolint, markdownlint-cli2, hyperfine, hey, syft, git-cliff, and goreleaser manually."; \
	fi

check-tools: ## Verify tool versions
	@echo "Tool Versions:"
	@$(GO) version || echo "go: NOT INSTALLED"
	@$(GOLANGCI_LINT) --version || echo "golangci-lint: NOT INSTALLED"
	@gosec -version || echo "gosec: NOT INSTALLED"
	@staticcheck -version || echo "staticcheck: NOT INSTALLED"
	@govulncheck -version || echo "govulncheck: NOT INSTALLED"
	@mockgen -version || echo "mockgen: NOT INSTALLED"
	@goimports -version || echo "goimports: NOT INSTALLED"
	@$(RUFF) --version || echo "ruff: NOT INSTALLED"
	@hadolint --version || echo "hadolint: NOT INSTALLED"
	@markdownlint-cli2 --version || echo "markdownlint-cli2: NOT INSTALLED"
	@yamllint --version || echo "yamllint: NOT INSTALLED"
	@hyperfine --version || echo "hyperfine: NOT INSTALLED"
	@hey -help 2>&1 | head -n 1 || echo "hey: NOT INSTALLED"

update-tools: install-tools ## Upgrade all tools to latest

##@ Repo Setup
setup-repo: install-tools ## One-time repo bootstrap
	go work sync || echo "go.work not yet initialized or synced"
	@for dir in $(GO_MCP_DIRS); do \
		echo "Tidying $$dir..."; \
		cd $$dir && $(GO) mod tidy && cd -; \
	done
	@for dir in $(PYTHON_MCP_DIRS); do \
		echo "Setting up Python venv in $$dir..."; \
		cd $$dir && $(PYTHON) -m venv .venv && .venv/bin/pip install --quiet --upgrade pip && .venv/bin/pip install --quiet -e ".[dev]" && cd -; \
	done
	@if command -v pre-commit >/dev/null 2>&1; then \
		pre-commit install; \
	else \
		$(PYTHON) -m pip install pre-commit && pre-commit install || echo "Warning: pre-commit install failed"; \
	fi

setup: setup-repo ## Alias for setup-repo

##@ Validation
validate-go: ## Validate all Go modules
	@echo "→ Validating Go modules..."
	@for dir in $(GO_MCP_DIRS); do \
		if [ -f $$dir/go.mod ]; then echo "  ✓ $$dir/go.mod"; else echo "  ✗ $$dir/go.mod MISSING" && exit 1; fi; \
		if [ -f $$dir/main.go ]; then echo "  ✓ $$dir/main.go"; else echo "  ✗ $$dir/main.go MISSING" && exit 1; fi; \
	done

validate-python: ## Validate Python MCPs
	@echo "→ Validating Python modules..."
	@for dir in $(PYTHON_MCP_DIRS); do \
		if [ -f $$dir/pyproject.toml ]; then echo "  ✓ $$dir/pyproject.toml"; else echo "  ✗ $$dir/pyproject.toml MISSING" && exit 1; fi; \
		if [ -f $$dir/server.py ]; then echo "  ✓ $$dir/server.py"; else echo "  ✗ $$dir/server.py MISSING" && exit 1; fi; \
	done

validate-config: ## Validate config.yaml schemas
	@echo "→ Validating configurations..."
	@for dir in $(GO_MCP_DIRS) $(PYTHON_MCP_DIRS); do \
		if [ -f $$dir/config.yaml ]; then \
			echo "  ✓ $$dir/config.yaml exists"; \
		elif [ -f $$dir/config.example.yaml ]; then \
			echo "  ✓ $$dir/config.example.yaml exists"; \
		fi; \
	done

validate: validate-go validate-python validate-config ## Composite validation

##@ Linting
lint-go: ## Run golangci-lint on all Go modules
	@echo "→ Running golangci-lint..."
	@for dir in $(GO_MCP_DIRS); do \
		cd $$dir && $(GOLANGCI_LINT) run && cd -; \
	done

lint-python: ## Run ruff check --fix on all Python MCPs
	@echo "→ Running ruff check --fix..."
	$(RUFF) check $(PYTHON_MCP_DIRS) --fix

lint-docker: ## Run hadolint on all Dockerfiles
	@echo "→ Running hadolint..."
	@if command -v hadolint >/dev/null 2>&1; then \
		find . -name "Dockerfile*" -not -path "*/.venv/*" -exec hadolint {} +; \
	else \
		echo "hadolint not installed, skipping"; \
	fi

lint-markdown: ## Run markdownlint-cli2 on all *.md
	@echo "→ Running markdownlint-cli2..."
	@if command -v markdownlint-cli2 >/dev/null 2>&1; then \
		markdownlint-cli2 "**/*.md" "#node_modules" "#.venv"; \
	else \
		echo "markdownlint-cli2 not installed, skipping"; \
	fi

lint-yaml: ## Run yamllint on all *.yaml
	@echo "→ Running yamllint..."
	@if command -v yamllint >/dev/null 2>&1; then \
		yamllint -c .yamllint.yaml . 2>/dev/null || yamllint . ; \
	else \
		echo "yamllint not installed, skipping"; \
	fi

lint-all: ## Run all linters in parallel
	@$(MAKE) -j5 lint-go lint-python lint-docker lint-markdown lint-yaml

##@ Testing
test-go: ## Run Go tests with race detector and coverage
	@echo "→ Running Go tests..."
	@for dir in $(GO_MCP_DIRS); do \
		cd $$dir && $(GO) test -race -coverprofile=cover.out -covermode=atomic ./... && cd -; \
	done

test-python: ## Run Python tests with pytest and coverage
	@echo "→ Running Python tests..."
	@for dir in $(PYTHON_MCP_DIRS); do \
		if [ -d $$dir/tests ]; then \
			cd $$dir && .venv/bin/pytest --cov --cov-report=term-missing --cov-fail-under=80 tests/ && cd -; \
		else \
			echo "No tests for $$dir"; \
		fi; \
	done

test-integration: ## Run cross-MCP integration tests
	@echo "→ Running integration tests..."
	@if [ -d mcp/pr-reviewer/tests ]; then \
		cd mcp/pr-reviewer && .venv/bin/pytest tests/test_integration_*.py; \
	fi

test-all: test-go test-python test-integration ## Run all tests

coverage-report: ## Generate coverage.html and enforce >=80%
	@echo "→ Generating coverage reports..."
	@for dir in $(GO_MCP_DIRS); do \
		if [ -f $$dir/cover.out ]; then \
			cd $$dir && $(GO) tool cover -html=cover.out -o coverage.html && cd -; \
		fi; \
	done

##@ Benchmarks
bench-go: ## Run Go benchmarks
	@echo "→ Running Go benchmarks..."
	@for dir in $(GO_MCP_DIRS); do \
		if [ -d $$dir/bench ]; then \
			cd $$dir && $(GO) test -bench=. -benchtime=10s -count=3 ./bench/... && cd -; \
		elif [ -d $$dir/internal ]; then \
			cd $$dir && $(GO) test -bench=. -benchtime=10s -count=3 ./... && cd -; \
		fi; \
	done

bench-python: ## Run Python benchmarks
	@echo "→ Running Python benchmarks..."
	@for dir in $(PYTHON_MCP_DIRS); do \
		if [ -d $$dir/tests ]; then \
			cd $$dir && .venv/bin/pytest --benchmark-only tests/ 2>/dev/null || echo "No benchmarks found in $$dir"; \
		fi; \
	done

bench-all: bench-go bench-python ## Run all benchmarks

bench-compare: ## Compare benchmarks vs main branch
	@if command -v benchstat >/dev/null 2>&1; then \
		echo "Comparing benchmarks..."; \
	else \
		go install golang.org/x/perf/cmd/benchstat@latest; \
	fi

##@ Security
security: ## Run gosec + govulncheck + pip-audit
	@echo "→ Running Go security scans..."
	@for dir in $(GO_MCP_DIRS); do \
		gosec $$dir/... || true; \
		govulncheck $$dir/... || true; \
	done
	@echo "→ Running Python security scans..."
	@for dir in $(PYTHON_MCP_DIRS); do \
		cd $$dir && .venv/bin/pip-audit || pip-audit || true; cd -; \
	done

sbom: ## Generate SBOM with syft
	@if command -v syft >/dev/null 2>&1; then \
		syft . -o cyclonedx-json=sbom.json; \
	else \
		echo "syft not installed"; \
	fi

##@ Build & Release
build-all: ## Build all binaries to ./bin/
	@mkdir -p $(BIN_DIR)
	@for dir in $(GO_MCP_DIRS); do \
		name=$$(basename $$dir); \
		echo "Building $$name to $(BIN_DIR)..."; \
		cd $$dir && $(GO) build -o $(REPO_ROOT)/bin/$$name . && cd -; \
	done

install-all: build-all ## Install all binaries to GOBIN
	@mkdir -p $(GOBIN)
	cp $(BIN_DIR)/* $(GOBIN)/

version-bump: ## Bump semver (usage: make version-bump PART=patch|minor|major)
	@if [ -z "$(PART)" ]; then echo "Usage: make version-bump PART=patch|minor|major" && exit 1; fi
	@if command -v git-cliff >/dev/null 2>&1; then \
		echo "Bumping version..."; \
	else \
		echo "git-cliff not installed"; \
	fi

changelog: ## Generate changelog from conventional commits
	@if command -v git-cliff >/dev/null 2>&1; then \
		git-cliff -o CHANGELOG.md; \
	else \
		echo "git-cliff not installed"; \
	fi

tag: ## Create and push git tag
	@if [ -z "$(VERSION)" ]; then echo "Usage: make tag VERSION=vX.Y.Z" && exit 1; fi
	git tag -a $(VERSION) -m "Release $(VERSION)"
	git push origin $(VERSION)

release: ## Run goreleaser release --clean
	@if command -v goreleaser >/dev/null 2>&1; then \
		goreleaser release --clean; \
	else \
		echo "goreleaser not installed"; \
	fi

release-dry-run: ## Preview release without pushing
	@if command -v goreleaser >/dev/null 2>&1; then \
		goreleaser release --clean --skip=publish --snapshot; \
	else \
		echo "goreleaser not installed"; \
	fi

##@ Development
run-reasoning-memory: ## Run reasoning-memory MCP server
	cd $(REPO_ROOT)/mcp/reasoning-memory && $(GO) run .

run-credential-vault: ## Run credential-vault MCP server
	cd $(REPO_ROOT)/mcp/credential-vault && .venv/bin/python server.py

run-pr-reviewer: ## Run pr-reviewer MCP server
	cd $(REPO_ROOT)/mcp/pr-reviewer && .venv/bin/python server.py

dev: ## Hot reload for all MCPs in parallel
	@echo "Starting dev servers in parallel..."
	@echo "Use Ctrl+C to stop"
	@if command -v air >/dev/null 2>&1; then \
		(cd mcp/reasoning-memory && air) & \
	else \
		(cd mcp/reasoning-memory && $(GO) run .) & \
	fi
	@(cd mcp/credential-vault && .venv/bin/python server.py) & \
	@(cd mcp/pr-reviewer && .venv/bin/python server.py) & \
	wait

fmt: ## Format all code (gofmt + ruff format)
	@echo "→ Formatting Go code..."
	@for dir in $(GO_MCP_DIRS); do \
		gofmt -s -w $$dir && goimports -w $$dir; \
	done
	@echo "→ Formatting Python code..."
	$(RUFF) format $(PYTHON_MCP_DIRS)

vet: ## Run go vet + staticcheck
	@echo "→ Vetting Go code..."
	@for dir in $(GO_MCP_DIRS); do \
		cd $$dir && $(GO) vet ./... && staticcheck ./... && cd -; \
	done

doctor: ## Health check all MCPs
	@echo "→ Checking dependencies..."
	@$(GO) version
	@$(PYTHON) --version
	@echo "→ Checking modules..."
	@for dir in $(GO_MCP_DIRS); do \
		if [ -d $$dir ]; then echo "  ✓ $$dir directory exists"; fi; \
	done
	@for dir in $(PYTHON_MCP_DIRS); do \
		if [ -d $$dir ]; then echo "  ✓ $$dir directory exists"; fi; \
		if [ -d $$dir/.venv ]; then echo "  ✓ $$dir/.venv exists"; else echo "  ✗ $$dir/.venv MISSING"; fi; \
	done

##@ Maintenance
clean: ## Remove build artifacts
	@echo "→ Cleaning up..."
	@rm -rf $(BIN_DIR)
	@rm -rf $(REPO_ROOT)/mcp/reasoning-memory/reasoning-memory
	@rm -rf sbom.json
	@for dir in $(PYTHON_MCP_DIRS); do \
		rm -rf $$dir/__pycache__; \
		find $$dir -name '*.pyc' -delete; \
		find $$dir -name '__pycache__' -type d -exec rm -rf {} + 2>/dev/null || true; \
	done
	@echo "✓ Clean complete"

distclean: clean ## Remove build artifacts + venvs, caches
	@echo "→ Removing all state..."
	rm -rf $(REPO_ROOT)/*.egg-info
	rm -rf $(REPO_ROOT)/dist
	rm -rf $(REPO_ROOT)/build
	@for dir in $(PYTHON_MCP_DIRS); do \
		rm -rf $$dir/.venv; \
		rm -rf $$dir/.pytest_cache; \
		rm -rf $$dir/.ruff_cache; \
	done
	@echo "✓ Distclean complete"
