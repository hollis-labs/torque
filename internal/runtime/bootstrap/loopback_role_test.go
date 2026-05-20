package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
)

// TestIsOrchestratorClassRole pins the documented role names that get the
// full cross-task MCP tool surface (CW-20260509-0018). Adding a new role
// to the orchestrator class here MUST be paired with consideration of
// the security implications: the named role gets tools that can read,
// transition, create, and update arbitrary tasks. Reserved profile names
// — operator MUST NOT use them as kind=agent worker profiles.
func TestIsOrchestratorClassRole(t *testing.T) {
	t.Run("orchestrator-class roles return true", func(t *testing.T) {
		for _, role := range []string{
			"orchestrator",       // planstart-driven plan walker
			"planner",            // planner sub-task spawned by orchestrator
			"reviewer-end-agent", // reviewer fired on review-transition
		} {
			assert.True(t, isOrchestratorClassRole(role), "role %q should be orchestrator-class", role)
		}
	})

	t.Run("worker-class roles return false", func(t *testing.T) {
		for _, role := range []string{
			"",      // empty = kind=agent worker, restricted subset
			"agent", // generic agent
			"torque-backend",
			"torque-frontend",
			"some-custom-profile",
		} {
			assert.False(t, isOrchestratorClassRole(role), "role %q should be worker-class", role)
		}
	})

	t.Run("variant casing is NOT auto-matched", func(t *testing.T) {
		// Roles are case-sensitive — operator must use the canonical form.
		// Documented as such in the LoopbackBuilder godoc.
		assert.False(t, isOrchestratorClassRole("Orchestrator"))
		assert.False(t, isOrchestratorClassRole("ORCHESTRATOR"))
		assert.False(t, isOrchestratorClassRole("Planner"))
	})

	t.Run("near-misses must NOT match", func(t *testing.T) {
		// Catch typos that would silently widen the loopback contract.
		for _, role := range []string{
			"orchestrators",   // plural
			"orchestrator-v2", // versioned
			"my-orchestrator", // prefixed
			"plan",            // truncated
			"reviewer",        // truncated
			"end-agent",       // truncated
		} {
			assert.False(t, isOrchestratorClassRole(role), "role %q should NOT match orchestrator-class", role)
		}
	})
}

// TestLoopbackBuilder_WorkerRequiresTaskID is the regression guard for the
// task-less worker session panic: a bare torque_session_launch / HTTP
// /sessions/launch with task_id omitted reached mcpadapter.NewLoopback("")
// and panicked the request handler ("NewLoopback requires non-empty
// taskID"). The worker branch now returns a clean error instead.
func TestLoopbackBuilder_WorkerRequiresTaskID(t *testing.T) {
	// service.New(nil) is safe — it only composes domain-service structs
	// around the (nil) store; the worker-branch guard returns before any
	// store access, so a nil-store service is sufficient for this test.
	build := loopbackBuilder(service.New(nil), nil, nil, nil)
	if build == nil {
		t.Fatal("loopbackBuilder returned nil for a non-nil service")
	}

	t.Run("worker role + empty taskID returns an error, not a panic", func(t *testing.T) {
		h, err := build("", "")
		assert.Error(t, err)
		assert.Nil(t, h)
		assert.Contains(t, err.Error(), "task_id")
	})

	t.Run("named worker profile + empty taskID also returns an error", func(t *testing.T) {
		h, err := build("", "torque-backend")
		assert.Error(t, err)
		assert.Nil(t, h)
	})

	t.Run("worker role + a real taskID still builds a handle", func(t *testing.T) {
		h, err := build("CW-20260516-9999", "")
		assert.NoError(t, err)
		if assert.NotNil(t, h) {
			// Bounded context so a stuck Shutdown fails the test instead
			// of hanging it; assert the clean-close path returns no error.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			assert.NoError(t, h.Shutdown(ctx))
		}
	})
}
