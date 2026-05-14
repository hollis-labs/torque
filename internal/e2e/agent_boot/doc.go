// Package agent_boot is the integration-test surface for the agent.Boot
// substrate (CW-20260508-0001 / CW-20260508-0002).
//
// Each test composes a real agent.Manager + real sqlstore + real bootdir
// planting against a fakeRuntime injected via Dependencies.RuntimeFactory.
// That gives tests the same code path the production daemon takes — minus
// the spawn — so per-Mode + per-feature behavior can be asserted without a
// real LLM or a real CLI binary.
//
// What lives here:
//
//   - fakeruntime.go      fakeRuntime + fakeSession (records StartOptions,
//     lets tests trigger TypedEventCallback synthetically,
//     drives PIDReporter for both PTY and adapter shapes)
//   - helpers.go          composeDeps(t, ...) → real deps wired against the
//     fakeRuntime; plantCheckpoint(...) for ModeResume
//   - boot_test.go        per-Mode coverage (LongLived / OneShot / Subagent /
//     Background / Resume)
//   - feature_test.go     per-feature coverage (PIDReporter / TypedEventCallback /
//     Supervisor / SupervisorPassThrough / ExitErrorCause /
//     SandboxAllowLoopback)
//   - plan_execute_test.go ports the legacy planstart → orchestrator-boot
//     smoke onto agent.Boot's AutoFireFirstTurn shape
//   - broker_test.go      ports the legacy substrate (sessionmgr + broker)
//     hello-world smoke onto agent.Manager
//
// Replaces the build-tagged internal/e2e/plan_execute/ + internal/e2e/
// sessionmgr_broker/ packages — both gated behind agentboot_e2e_pending
// after the CW-20260508-0001 rewrite deleted sessionmgr.Manager. The new
// package consolidates substrate-composition coverage in one place.
package agent_boot
