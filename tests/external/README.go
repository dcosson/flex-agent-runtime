// Package external contains end-to-end tests for the flex-agent-runtime
// organized in three tiers:
//
//   - tier1: Mock-based E2E tests and cross-mode parity. Run on every PR,
//     no infrastructure dependencies. Build tag: (none).
//
//   - tier2: Docker-based tests with real sandbox-host containers.
//     Supports minimal (local-disk + none), gVisor (local-disk + gvisor),
//     and full (ZFS + gVisor) configurations. Build tag: docker.
//
//   - tier3: Native host tests with real ZFS, gVisor, and provider APIs.
//     Run nightly on dedicated Linux hosts. Build tag: native.
//
// The common/ package provides shared helpers: prerequisite checks,
// backend config definitions, reporting types, and parity comparison.
//
// See docs/plans/17-external-testing.md for the full test strategy.
package external
