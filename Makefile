GO ?= go
PKGS := $(shell $(GO) list ./...)

.PHONY: help build fmt fmt-check vet deps-staticcheck check test test-race test-harness test-harness-t2 test-harness-openai test-harness-google test-harness-codeinterp test-harness-e2e-codeinterp test-bench test-bench-ai-core test-bench-openai test-bench-google test-bench-codeinterp test-stress-openai test-stress-google test-stress-codeinterp test-stress-e2e-codeinterp test-fuzz test-fuzz-t2 test-anthropic-harness-fast test-anthropic-harness-race test-anthropic-harness-bench test-e2e clean

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

test-harness-openai: ## Run OpenAI provider harness tests (P*/F*/S*/SEC*/O* patterns)
	$(GO) test -race ./internal/ai/provider/openai/ -run 'Test(P[1-6]_|F[1-6]_|S[2-4]_|SEC[1-3]_|O3_)'

test-harness-google: ## Run Google provider harness tests (P*/F*/S*/GS*/SEC*/EC* patterns)
	$(GO) test -race ./internal/ai/provider/google/ -run 'Test(P[1-6]_|F[1-5]_|S[1-2]_|GS[1-5]_|SEC[1-3]_|EC1_)'

test-harness-codeinterp: ## Run Code Interpreter harness suites (P/F/O/S/ST/SEC lanes)
	$(GO) test -race ./internal/tools/codeinterp -run 'Test(P[1-8]_|F[1-8]_|O[1-5]_|S[1-6]_|ST[1-5]_|SEC[1-7]_)'

test-harness-e2e-codeinterp: ## Run Code Interpreter E2E harness suites (P/F/O/S/B/ST/SEC lanes)
	$(GO) test -race ./e2etests/scenarios -run 'Test(Scenario_CodeInterpreterWorkflow|P[1-5]_|F[1-5]_|O[1-3]_|S[1-3]_|B[1-4]_|ST[1-3]_|SEC[1-4]_)'

test-bench: ## Run benchmark suite (B* targets)
	$(GO) test ./... -bench . -benchmem

test-bench-ai-core: ## Run B1-B7 ai-core benchmark set and write baseline snapshot
	@mkdir -p docs/benchmarks
	$(GO) test ./internal/ai/... -run '^$$' -bench 'Benchmark(EventStreamSendReceive|Scanner100Events|TransformMessages|ValidateToolArguments|SchemaCompilationCold|CalculateCost|GetModel)$$' -benchmem | tee docs/benchmarks/01-ai-core-baseline.txt

test-bench-openai: ## Run OpenAI provider benchmarks (B1, B3, B5, B6)
	@mkdir -p docs/benchmarks
	$(GO) test ./internal/ai/provider/openai/ -run '^$$' -bench 'BenchmarkB[1356]_' -benchmem | tee docs/benchmarks/03-openai-provider-baseline.txt

test-stress-openai: ## Run OpenAI provider stress/soak tests (ST1-ST3)
	$(GO) test -race ./internal/ai/provider/openai/ -run 'TestST[1-3]_' -count=1

test-bench-google: ## Run Google provider benchmarks (B1-B4)
	@mkdir -p docs/benchmarks
	$(GO) test ./internal/ai/provider/google/ -run '^$$' -bench 'BenchmarkB[1-4]_' -benchmem | tee docs/benchmarks/04-google-provider-baseline.txt

test-bench-codeinterp: ## Run Code Interpreter benchmarks (B1-B6)
	@mkdir -p docs/benchmarks
	$(GO) test ./internal/tools/codeinterp -run '^$$' -bench 'BenchmarkB[1-6]_' -benchmem | tee docs/benchmarks/07-codeinterp-baseline.txt

test-stress-google: ## Run Google provider stress/soak tests (SK1-SK3)
	$(GO) test -race ./internal/ai/provider/google/ -run 'TestSK[1-3]_' -count=1

test-stress-codeinterp: ## Run Code Interpreter stress/soak tests (ST1-ST5)
	$(GO) test -race ./internal/tools/codeinterp -run 'TestST[1-5]_' -count=1

test-stress-e2e-codeinterp: ## Run Code Interpreter E2E stress/soak tests (ST1-ST3)
	$(GO) test -race ./e2etests/scenarios -run 'TestST[1-3]_' -count=1

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

test-anthropic-harness-fast: ## Anthropic provider harness: property + stub/fault + deterministic/security lanes
	$(GO) test ./internal/ai/provider/anthropic -run 'Test(P|S|F|D|SEC|O|ManualQA|HarnessCoverage)' -count=1

test-anthropic-harness-race: ## Anthropic provider harness with race detector
	$(GO) test -race ./internal/ai/provider/anthropic -run 'Test(P|S|F|D|SEC)' -skip 'TestST1_LongSoak|TestST3_BurstToolStress' -count=1

test-anthropic-harness-bench: ## Anthropic provider harness benchmark lanes B1-B4
	$(GO) test ./internal/ai/provider/anthropic -run '^$$' -bench 'BenchmarkB[1-4]_' -benchmem

test-e2e: ## Run E2E agent + tools scenarios
	$(GO) test -race ./e2etests/... -count=1 -timeout 120s

clean: ## Remove temporary test artifacts
	rm -f coverage.out coverage.html
