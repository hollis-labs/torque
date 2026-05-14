package scheduler_test

import (
	"testing"

	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEscalationNextStep(t *testing.T) {
	engine := scheduler.NewEscalationEngine()

	chain := []string{"retry", "senior-agent", "council", "human"}

	action := engine.NextAction(chain, 0)
	assert.Equal(t, "retry", action.Strategy)
	assert.Equal(t, 1, action.NextStep)
	assert.False(t, action.Exhausted)
}

func TestEscalationWalksChain(t *testing.T) {
	engine := scheduler.NewEscalationEngine()

	chain := []string{"retry", "senior-agent", "human"}

	a1 := engine.NextAction(chain, 0)
	assert.Equal(t, "retry", a1.Strategy)
	assert.Equal(t, 1, a1.NextStep)

	a2 := engine.NextAction(chain, 1)
	assert.Equal(t, "senior-agent", a2.Strategy)
	assert.Equal(t, 2, a2.NextStep)

	a3 := engine.NextAction(chain, 2)
	assert.Equal(t, "human", a3.Strategy)
	assert.Equal(t, 3, a3.NextStep)
}

func TestEscalationExhausted(t *testing.T) {
	engine := scheduler.NewEscalationEngine()

	chain := []string{"retry", "human"}

	action := engine.NextAction(chain, 2)
	assert.True(t, action.Exhausted)
	assert.Equal(t, "block", action.Strategy)
}

func TestEscalationEmptyChain(t *testing.T) {
	engine := scheduler.NewEscalationEngine()

	action := engine.NextAction(nil, 0)
	assert.True(t, action.Exhausted)
	assert.Equal(t, "block", action.Strategy)
}

func TestEscalationResolveRetry(t *testing.T) {
	engine := scheduler.NewEscalationEngine()

	resolution, err := engine.Resolve(scheduler.EscalationAction{
		Strategy: "retry",
		NextStep: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, "todo", resolution.NewStatus)
	assert.Equal(t, 1, resolution.NewEscalationStep)
}

func TestEscalationResolveSeniorAgent(t *testing.T) {
	engine := scheduler.NewEscalationEngine()

	resolution, err := engine.Resolve(scheduler.EscalationAction{
		Strategy: "senior-agent",
		NextStep: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, "todo", resolution.NewStatus)
	assert.True(t, resolution.ChangeAgentProfile)
	assert.Equal(t, "senior", resolution.AgentProfile)
}

func TestEscalationResolveHuman(t *testing.T) {
	engine := scheduler.NewEscalationEngine()

	resolution, err := engine.Resolve(scheduler.EscalationAction{
		Strategy: "human",
		NextStep: 3,
	})
	require.NoError(t, err)
	assert.Equal(t, "blocked", resolution.NewStatus)
	assert.Equal(t, "Escalated to human review", resolution.BlockedReason)
}

func TestEscalationResolveBlock(t *testing.T) {
	engine := scheduler.NewEscalationEngine()

	resolution, err := engine.Resolve(scheduler.EscalationAction{
		Strategy:  "block",
		Exhausted: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "blocked", resolution.NewStatus)
	assert.Equal(t, "Escalation chain exhausted", resolution.BlockedReason)
}

func TestEscalationResolveCouncil(t *testing.T) {
	engine := scheduler.NewEscalationEngine()

	resolution, err := engine.Resolve(scheduler.EscalationAction{
		Strategy: "council",
		NextStep: 3,
	})
	require.NoError(t, err)
	assert.Equal(t, "todo", resolution.NewStatus)
	assert.True(t, resolution.ChangeAgentProfile)
	assert.Equal(t, "council", resolution.AgentProfile)
}
