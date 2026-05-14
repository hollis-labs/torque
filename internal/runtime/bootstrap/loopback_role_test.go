package bootstrap

import (
	"testing"

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
