GO ?= go
PKGS := $(shell $(GO) list ./...)

.PHONY: help build fmt fmt-check vet deps-staticcheck check test test-race test-harness test-harness-t2 test-bench test-bench-ai-core test-fuzz test-fuzz-t2 test-e2e clean

help: ## Show available make targets
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make <target>\n\nTargets:\n"} /^[a-zA-Z0-9_.-]+:.*##/ {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build all project packages
	$(GO) build ./...

fmt: ## Run gofmt on all Go files (writes changes)
	gofmt -w .

fmt-check: ## Check gofmt formatting without modifying files
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "above files are not formatted" && exit 1)

vet: ## Run go vet across all packages
	$(GO) vet ./...

deps-staticcheck: ## Install staticcheck dependency
	$(GO) install honnef.co/go/tools/cmd/staticcheck@latest

check: fmt vet ## Run formatting, vet, and staticcheck
	@echo "==> staticcheck"
	$(GO) run honnef.co/go/tools/cmd/staticcheck@latest ./...

test: ## Run quick test suite (unit + small integration per package)
	$(GO) test ./...

test-race: ## Run full test suite with race detector
	$(GO) test -race ./...

test-harness: ## Run harness-focused tests (P*/D*/S* patterns currently implemented)
	$(GO) test ./... -run 'Test(EventStreamOrdering|EventStreamAlwaysTerminates|EventStreamSlowConsumer|EventStreamCancelMidStream|EventStreamHighThroughput|TransformIdempotency|TransformMessageCountBound|TransformDeterministic|CostConsistencyRapid|RegistryConcurrentAccess|RegistryStressContention)'

test-harness-t2: ## Run T2 thorough property test tier (10K rapid checks)
	$(GO) test ./internal/ai -rapid.checks=10000 -run 'Test(EventStreamOrdering|TransformIdempotency|CostConsistencyRapid|P4_CoerceTypesPreservesValidTypes|EventStreamAlwaysTerminates|TransformMessageCountBound|ModelRegistryMutationIsolation|TransformDeterministic|RegistryConcurrentAccess)'

test-bench: ## Run benchmark suite (B* targets)
	$(GO) test ./... -bench . -benchmem

test-bench-ai-core: ## Run B1-B7 ai-core benchmark set and write baseline snapshot
	@mkdir -p docs/benchmarks
	$(GO) test ./internal/ai/... -run '^$$' -bench 'Benchmark(EventStreamSendReceive|Scanner100Events|TransformMessages|ValidateToolArguments|SchemaCompilationCold|CalculateCost|GetModel)$$' -benchmem | tee docs/benchmarks/01-ai-core-baseline.txt

test-fuzz: ## Run short fuzz checks for parser/overflow fuzz targets
	$(GO) test ./internal/ai/sse -run '^$$' -fuzz FuzzScanner -fuzztime=5s
	$(GO) test ./internal/ai -run '^$$' -fuzz FuzzValidateToolArguments -fuzztime=5s
	$(GO) test ./internal/ai -run '^$$' -fuzz FuzzCoerceTypes -fuzztime=5s
	$(GO) test ./internal/ai -run '^$$' -fuzz FuzzIsContextOverflow -fuzztime=5s

test-fuzz-t2: ## Run T2 thorough fuzz tier (30s per target)
	$(GO) test ./internal/ai/sse -run '^$$' -fuzz FuzzScanner -fuzztime=30s
	$(GO) test ./internal/ai -run '^$$' -fuzz FuzzValidateToolArguments -fuzztime=30s
	$(GO) test ./internal/ai -run '^$$' -fuzz FuzzCoerceTypes -fuzztime=30s
	$(GO) test ./internal/ai -run '^$$' -fuzz FuzzIsContextOverflow -fuzztime=30s

test-e2e: ## Placeholder for future e2e suites
	@echo "No e2e test packages yet."

clean: ## Remove temporary test artifacts
	rm -f coverage.out coverage.html
