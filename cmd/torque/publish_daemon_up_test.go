package main

import (
	"os"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPublishDaemonUpEmitsExpectedPayload verifies the on-boot
// `daemon.up` emission published by publishDaemonUp. Payload contract
// (CW-20260519-0126):
//
//   - pid is os.Getpid()
//   - started_at is an RFC3339Nano UTC timestamp
//   - binary_path / binary_mtime are best-effort; present on test
//     binaries because go test invokes via a real executable path that
//     os.Executable + os.Stat can resolve.
//
// The test subscribes to the EventBus BEFORE publishing so the in-process
// fan-out has a live consumer and the buffered channel does not race
// with the publish call.
func TestPublishDaemonUpEmitsExpectedPayload(t *testing.T) {
	bus := scheduler.NewEventBus()
	defer bus.Close()

	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	publishDaemonUp(bus)

	var ev scheduler.SchedulerEvent
	select {
	case ev = <-sub:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for daemon.up event")
	}

	assert.Equal(t, "daemon.up", ev.Type)

	payload, ok := ev.Data.(map[string]interface{})
	require.True(t, ok, "Data should be map[string]interface{}, got %T", ev.Data)

	// pid: native int from os.Getpid()
	pidVal, ok := payload["pid"]
	require.True(t, ok, "payload must include pid")
	pid, ok := pidVal.(int)
	require.True(t, ok, "pid should be int, got %T", pidVal)
	assert.Equal(t, os.Getpid(), pid, "pid must match the running process")

	// started_at: RFC3339Nano-parseable string
	startedAtRaw, ok := payload["started_at"]
	require.True(t, ok, "payload must include started_at")
	startedAt, ok := startedAtRaw.(string)
	require.True(t, ok, "started_at should be string, got %T", startedAtRaw)
	parsed, err := time.Parse(time.RFC3339Nano, startedAt)
	require.NoError(t, err, "started_at must be RFC3339Nano-parseable")
	assert.Equal(t, time.UTC, parsed.Location(), "started_at must be UTC")

	// binary_path / binary_mtime are best-effort. On a normal `go test`
	// invocation os.Executable resolves to the test binary, so both
	// should land. Treat absence as a soft-skip rather than a hard fail
	// to keep the test resilient on exotic environments where the
	// platform refuses os.Executable (the production code path tolerates
	// that case silently, per the function's contract comment).
	if exe, exeErr := os.Executable(); exeErr == nil && exe != "" {
		bp, ok := payload["binary_path"].(string)
		require.True(t, ok, "binary_path should be string when present")
		assert.NotEmpty(t, bp, "binary_path should not be empty when published")

		bmRaw, ok := payload["binary_mtime"]
		require.True(t, ok, "binary_mtime should be present alongside binary_path")
		bm, ok := bmRaw.(string)
		require.True(t, ok, "binary_mtime should be string, got %T", bmRaw)
		mtParsed, err := time.Parse(time.RFC3339Nano, bm)
		require.NoError(t, err, "binary_mtime must be RFC3339Nano-parseable")
		assert.Equal(t, time.UTC, mtParsed.Location(), "binary_mtime must be UTC")
	}
}

// TestPublishDaemonUpNilBusIsNoOp pins the early-return guard so a future
// caller wiring publishDaemonUp before the EventBus is constructed does
// not panic. (The ! nil guard exists in the function but is otherwise
// untested.)
func TestPublishDaemonUpNilBusIsNoOp(t *testing.T) {
	// Should not panic.
	publishDaemonUp(nil)
}
