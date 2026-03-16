GO ?= go
PKGS := $(shell $(GO) list ./...)

.PHONY: help build fmt fmt-check vet deps-staticcheck check test test-race test-e2e \
	test-harness test-harness-t2 \
	test-anthropic-harness-fast test-anthropic-harness-race \
	test-harness-openai test-harness-google \
	test-harness-codeinterp test-harness-e2e-codeinterp \
	test-harness-mode3-fast test-harness-mode3-standard test-harness-mode3-nightly \
	test-harness-runtime-fast test-harness-runtime-standard test-harness-runtime-nightly test-harness-runtime-weekly \
	test-bench test-bench-ai-core test-anthropic-harness-bench test-bench-openai test-bench-google test-bench-codeinterp \
	test-stress-openai test-stress-google test-stress-codeinterp test-stress-e2e-codeinterp \
	test-fuzz test-fuzz-t2 \
	test-external-tier1 test-external-tier2 test-external-tier3 \
	clean

help:
	@echo "h2-agent-runtime build & test targets"
	@echo ""
	@echo "=== Build & Check ==="
	@echo "  build                            Build all project packages"
	@echo "  fmt                              Run gofmt on all Go files (writes changes)"
	@echo "  fmt-check                        Check gofmt formatting without modifying files"
	@echo "  vet                              Run go vet across all packages"
	@echo "  check                            Run formatting, vet, and staticcheck"
	@echo "  deps-staticcheck                 Install staticcheck dependency"
	@echo ""
	@echo "=== Core Tests ==="
	@echo "  test                             Unit + small integration (all packages)"
	@echo "  test-race                        Full test suite with race detector"
	@echo "  test-e2e                         E2E agent + tools scenarios"
	@echo ""
	@echo "=== Harness Tests ==="
	@echo "  test-harness                     AI-core harness (P*/D*/S* property tests)"
	@echo "  test-harness-t2                  AI-core T2 thorough property tier (10K rapid checks)"
	@echo "  test-anthropic-harness-fast      Anthropic harness: property + stub/fault + deterministic/security"
	@echo "  test-anthropic-harness-race      Anthropic harness with race detector"
	@echo "  test-harness-openai              OpenAI harness (P*/F*/S*/SEC*/O* patterns)"
	@echo "  test-harness-google              Google harness (P*/F*/S*/GS*/SEC*/EC* patterns)"
	@echo "  test-harness-codeinterp          Code Interpreter harness (P/F/O/S/ST/SEC lanes)"
	@echo "  test-harness-e2e-codeinterp      Code Interpreter E2E harness (P/F/O/S/B/ST/SEC lanes)"
	@echo "  test-harness-mode3-fast          Mode 3 harness PR-fast (~15s): property/simulation/security smoke"
	@echo "  test-harness-mode3-standard      Mode 3 harness PR-standard (~45s): full P/F/O/S/SEC suites"
	@echo "  test-harness-mode3-nightly       Mode 3 harness nightly (~2h): chaos + stress + benches"
	@echo "  test-harness-runtime-fast        Runtime harness PR-fast (~5m): load scaling + baseline smoke"
	@echo "  test-harness-runtime-standard    Runtime harness PR-standard (~30m): deterministic runtime families"
	@echo "  test-harness-runtime-nightly     Runtime harness nightly (~4h): benchmarks + soak checks"
	@echo "  test-harness-runtime-weekly      Runtime harness weekly (~36h): long soak + full benchmarks"
	@echo ""
	@echo "=== Benchmarks ==="
	@echo "  test-bench                       Run all benchmarks"
	@echo "  test-bench-ai-core               AI-core benchmarks (B1-B7) + baseline snapshot"
	@echo "  test-anthropic-harness-bench     Anthropic benchmarks (B1-B4)"
	@echo "  test-bench-openai                OpenAI benchmarks (B1, B3, B5, B6) + baseline snapshot"
	@echo "  test-bench-google                Google benchmarks (B1-B4) + baseline snapshot"
	@echo "  test-bench-codeinterp            Code Interpreter benchmarks (B1-B6) + baseline snapshot"
	@echo ""
	@echo "=== Stress / Soak ==="
	@echo "  test-stress-openai               OpenAI stress/soak tests (ST1-ST3)"
	@echo "  test-stress-google               Google stress/soak tests (SK1-SK3)"
	@echo "  test-stress-codeinterp           Code Interpreter stress/soak tests (ST1-ST5)"
	@echo "  test-stress-e2e-codeinterp       Code Interpreter E2E stress/soak tests (ST1-ST3)"
	@echo ""
	@echo "=== Fuzz ==="
	@echo "  test-fuzz                        Short fuzz checks (5s per target)"
	@echo "  test-fuzz-t2                     Thorough fuzz tier (30s per target)"
	@echo ""
	@echo "=== External Tests ==="
	@echo "  test-external-tier1              Tier 1: mock + stubserver-based, runs anywhere (<30s)"
	@echo "  test-external-tier2              Tier 2: Docker-based sandbox-host (build tag: docker)"
	@echo "  test-external-tier3              Tier 3: native host (build tag: native, requires ZFS/gVisor)"
	@echo ""
	@echo "=== Cleanup ==="
	@echo "  clean                            Remove temporary test artifacts"

# ---------------------------------------------------------------------------
# Build & Check
# ---------------------------------------------------------------------------

build:
	$(GO) build ./...

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "above files are not formatted" && exit 1)

vet:
	$(GO) vet ./...

deps-staticcheck:
	$(GO) install honnef.co/go/tools/cmd/staticcheck@latest

check: fmt vet
	@echo "==> staticcheck"
	$(GO) run honnef.co/go/tools/cmd/staticcheck@latest ./...

# ---------------------------------------------------------------------------
# Core Tests
# ---------------------------------------------------------------------------

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

test-e2e:
	$(GO) test -race ./e2etests/... -count=1 -timeout 120s

# ---------------------------------------------------------------------------
# Harness Tests
# ---------------------------------------------------------------------------

test-harness:
	$(GO) test ./... -run 'Test(EventStreamOrdering|EventStreamAlwaysTerminates|EventStreamSlowConsumer|EventStreamCancelMidStream|EventStreamHighThroughput|TransformIdempotency|TransformMessageCountBound|TransformDeterministic|CostConsistencyRapid|RegistryConcurrentAccess|RegistryStressContention)'

test-harness-t2:
	$(GO) test ./internal/ai -rapid.checks=10000 -run 'Test(EventStreamOrdering|TransformIdempotency|CostConsistencyRapid|P4_CoerceTypesPreservesValidTypes|EventStreamAlwaysTerminates|TransformMessageCountBound|ModelRegistryMutationIsolation|TransformDeterministic|RegistryConcurrentAccess)'

test-anthropic-harness-fast:
	$(GO) test ./internal/ai/provider/anthropic -run 'Test(P|S|F|D|SEC|O|ManualQA|HarnessCoverage)' -count=1

test-anthropic-harness-race:
	$(GO) test -race ./internal/ai/provider/anthropic -run 'Test(P|S|F|D|SEC)' -skip 'TestST1_LongSoak|TestST3_BurstToolStress' -count=1

test-harness-openai:
	$(GO) test -race ./internal/ai/provider/openai/ -run 'Test(P[1-6]_|F[1-6]_|S[2-4]_|SEC[1-3]_|O3_)'

test-harness-google:
	$(GO) test -race ./internal/ai/provider/google/ -run 'Test(P[1-6]_|F[1-5]_|S[1-2]_|GS[1-5]_|SEC[1-3]_|EC1_)'

test-harness-codeinterp:
	$(GO) test -race ./internal/tools/codeinterp -run 'Test(P[1-8]_|F[1-8]_|O[1-5]_|S[1-6]_|ST[1-5]_|SEC[1-7]_)'

test-harness-e2e-codeinterp:
	$(GO) test -race ./e2etests/scenarios -run 'Test(Scenario_CodeInterpreterWorkflow|P[1-5]_|F[1-5]_|O[1-3]_|S[1-3]_|B[1-4]_|ST[1-3]_|SEC[1-4]_)'

test-harness-mode3-fast:
	MODE3_HARNESS_TIER=pr-fast $(GO) test -race ./e2etests/mode3/harness -run 'Test(P[1-2]_|S1_|SEC1_)'

test-harness-mode3-standard:
	MODE3_HARNESS_TIER=pr-standard $(GO) test -race ./e2etests/mode3/harness -run 'Test(P[1-5]_|F[1-5]_|O[1-3]_|S[1-3]_|SEC[1-4]_)'

test-harness-mode3-nightly:
	MODE3_HARNESS_TIER=nightly $(GO) test -race ./e2etests/mode3/harness -run 'Test(F[1-8]_|ST[1-3]_|SEC[1-4]_)' -count=1
	MODE3_HARNESS_TIER=nightly $(GO) test ./e2etests/mode3/harness -run '^$$' -bench 'BenchmarkB[1-4]_' -benchmem

test-harness-runtime-fast:
	$(GO) test -race ./e2etests/runtime/tests -run 'TestLoadScaling_PSmallAndPMedium|TestLoadScaling_BaselineComparisonAndGating|TestHostConfig_LoadsFleetConfig'

test-harness-runtime-standard:
	$(GO) test -race ./e2etests/runtime/tests -run 'TestLoadScaling_|TestSoakStability_|TestSnapshotGrowth_|TestContainerBootBenchmark_|TestRPCLatencyProfiling_|TestHostConfig_'

test-harness-runtime-nightly:
	$(GO) test -race ./e2etests/runtime/tests -run 'TestLoadScaling_|TestSoakStability_|TestSnapshotGrowth_|TestContainerBootBenchmark_|TestRPCLatencyProfiling_|TestHostConfig_' -count=1
	$(GO) test ./e2etests/runtime/tests -run '^$$' -bench 'BenchmarkContainerBoot_' -benchmem

test-harness-runtime-weekly:
	RUNTIME_HARNESS_TIER=weekly MODE3_ENABLE_SOAK=1 $(GO) test -race ./e2etests/runtime/tests -run 'TestSoakStability_|TestSnapshotGrowth_|TestRPCLatencyProfiling_|TestHostConfig_' -count=1
	$(GO) test ./e2etests/runtime/tests -run '^$$' -bench 'BenchmarkContainerBoot_' -benchmem

# ---------------------------------------------------------------------------
# Benchmarks
# ---------------------------------------------------------------------------

test-bench:
	$(GO) test ./... -bench . -benchmem

test-bench-ai-core:
	@mkdir -p docs/benchmarks
	$(GO) test ./internal/ai/... -run '^$$' -bench 'Benchmark(EventStreamSendReceive|Scanner100Events|TransformMessages|ValidateToolArguments|SchemaCompilationCold|CalculateCost|GetModel)$$' -benchmem | tee docs/benchmarks/01-ai-core-baseline.txt

test-anthropic-harness-bench:
	$(GO) test ./internal/ai/provider/anthropic -run '^$$' -bench 'BenchmarkB[1-4]_' -benchmem

test-bench-openai:
	@mkdir -p docs/benchmarks
	$(GO) test ./internal/ai/provider/openai/ -run '^$$' -bench 'BenchmarkB[1356]_' -benchmem | tee docs/benchmarks/03-openai-provider-baseline.txt

test-bench-google:
	@mkdir -p docs/benchmarks
	$(GO) test ./internal/ai/provider/google/ -run '^$$' -bench 'BenchmarkB[1-4]_' -benchmem | tee docs/benchmarks/04-google-provider-baseline.txt

test-bench-codeinterp:
	@mkdir -p docs/benchmarks
	$(GO) test ./internal/tools/codeinterp -run '^$$' -bench 'BenchmarkB[1-6]_' -benchmem | tee docs/benchmarks/07-codeinterp-baseline.txt

# ---------------------------------------------------------------------------
# Stress / Soak
# ---------------------------------------------------------------------------

test-stress-openai:
	$(GO) test -race ./internal/ai/provider/openai/ -run 'TestST[1-3]_' -count=1

test-stress-google:
	$(GO) test -race ./internal/ai/provider/google/ -run 'TestSK[1-3]_' -count=1

test-stress-codeinterp:
	$(GO) test -race ./internal/tools/codeinterp -run 'TestST[1-5]_' -count=1

test-stress-e2e-codeinterp:
	$(GO) test -race ./e2etests/scenarios -run 'TestST[1-3]_' -count=1

# ---------------------------------------------------------------------------
# Fuzz
# ---------------------------------------------------------------------------

test-fuzz:
	$(GO) test ./internal/ai/sse -run '^$$' -fuzz FuzzScanner -fuzztime=5s
	$(GO) test ./internal/ai -run '^$$' -fuzz FuzzValidateToolArguments -fuzztime=5s
	$(GO) test ./internal/ai -run '^$$' -fuzz FuzzCoerceTypes -fuzztime=5s
	$(GO) test ./internal/ai -run '^$$' -fuzz FuzzIsContextOverflow -fuzztime=5s

test-fuzz-t2:
	$(GO) test ./internal/ai/sse -run '^$$' -fuzz FuzzScanner -fuzztime=30s
	$(GO) test ./internal/ai -run '^$$' -fuzz FuzzValidateToolArguments -fuzztime=30s
	$(GO) test ./internal/ai -run '^$$' -fuzz FuzzCoerceTypes -fuzztime=30s
	$(GO) test ./internal/ai -run '^$$' -fuzz FuzzIsContextOverflow -fuzztime=30s

# ---------------------------------------------------------------------------
# External Tests
# ---------------------------------------------------------------------------

test-external-tier1:
	$(GO) test -v -timeout=2m ./e2etests/external/tier1/...

test-external-tier2:
	$(GO) test -v -tags=docker -timeout=5m ./e2etests/external/tier2/...

test-external-tier3:
	$(GO) test -v -tags=native -timeout=20m ./e2etests/external/tier3/...

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------

clean:
	rm -f coverage.out coverage.html
