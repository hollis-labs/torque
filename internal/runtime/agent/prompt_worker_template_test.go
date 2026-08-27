package agent

import (
	"strings"
	"testing"

	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShouldApplyWorkerTemplate exercises the role/mode predicate that gates
// the default-worker boot contract prepend. Two conditions must hold:
// ModeLongLived AND not orchestrator-class. The matrix below pins the
// expected behavior — adding a new mode or orchestrator role here should
// produce a deliberate test update.
func TestShouldApplyWorkerTemplate(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		want bool
	}{
		{
			name: "ModeLongLived + worker profile → apply",
			opts: Options{Mode: ModeLongLived, AgentProfile: "implementer"},
			want: true,
		},
		{
			name: "ModeLongLived + role override worker → apply",
			opts: Options{Mode: ModeLongLived, AgentProfile: "torque-backend", Role: "implementer"},
			want: true,
		},
		{
			name: "ModeLongLived + role=orchestrator → skip (has own template)",
			opts: Options{Mode: ModeLongLived, Role: "orchestrator"},
			want: false,
		},
		{
			name: "ModeLongLived + role=planner → skip (has own template)",
			opts: Options{Mode: ModeLongLived, Role: "planner"},
			want: false,
		},
		{
			name: "ModeLongLived + role=reviewer-end-agent → skip (has own template)",
			opts: Options{Mode: ModeLongLived, Role: "reviewer-end-agent"},
			want: false,
		},
		{
			name: "ModeLongLived + role empty falls back to AgentProfile=orchestrator → skip",
			opts: Options{Mode: ModeLongLived, AgentProfile: "orchestrator"},
			want: false,
		},
		{
			name: "ModeOneShot → skip regardless of role",
			opts: Options{Mode: ModeOneShot, AgentProfile: "implementer"},
			want: false,
		},
		{
			name: "ModeSubagent → skip (subagent contract is distinct)",
			opts: Options{Mode: ModeSubagent, AgentProfile: "implementer"},
			want: false,
		},
		{
			name: "ModeBackground → skip (fire-and-forget contract is distinct)",
			opts: Options{Mode: ModeBackground, AgentProfile: "implementer"},
			want: false,
		},
		{
			name: "ModeResume → skip (resumed session inherits the original transcript framing)",
			opts: Options{Mode: ModeResume, AgentProfile: "implementer"},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, shouldApplyWorkerTemplate(tc.opts))
		})
	}
}

// TestComposeSystemPromptPrependsDefaultWorkerTemplate confirms the worker
// boot contract lands in the assembled prompt for a ModeLongLived worker.
// The check is anchor-string-based (rather than full-text) so future
// editorial tweaks to the template don't break this test — the contract
// is "the substrate's default-worker contract is present and applied
// BEFORE per-task framing", not "matches an exact byte sequence".
func TestComposeSystemPromptPrependsDefaultWorkerTemplate(t *testing.T) {
	const taskFraming = "[TASK-FRAMING] do the thing"
	prompt := composeSystemPrompt(Options{
		Mode:         ModeLongLived,
		AgentProfile: "implementer",
		SystemPrompt: taskFraming,
	}, nil)

	require.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "Worker — long-lived dispatch contract", "default-worker template anchor must appear")
	assert.Contains(t, prompt, `What "done" means`, "completion contract section must appear")
	assert.Contains(t, prompt, "torque_task_checkpoint_emit", "help-asking primitive must be documented")
	assert.Contains(t, prompt, "torque_task_review", "completion call must be documented")

	// Stack order: worker template precedes per-task framing precedes
	// checkpoint redispatch.
	workerIdx := strings.Index(prompt, "Worker — long-lived dispatch contract")
	taskIdx := strings.Index(prompt, taskFraming)
	cpIdx := strings.Index(prompt, "Checkpoint redispatch protocol")
	require.NotEqual(t, -1, workerIdx, "worker template anchor missing")
	require.NotEqual(t, -1, taskIdx, "task framing missing")
	require.NotEqual(t, -1, cpIdx, "checkpoint redispatch protocol missing")
	assert.Less(t, workerIdx, taskIdx, "worker template must precede task framing")
	assert.Less(t, taskIdx, cpIdx, "task framing must precede checkpoint redispatch")
}

// TestComposeSystemPromptSkipsWorkerTemplateForOrchestrator verifies the
// orchestrator's prompt does NOT carry the worker boot contract. The
// orchestrator has its own template baked into the task system_prompt;
// stacking the worker contract on top would be redundant + confusing
// (commit-and-PR instructions on a session whose job is to dispatch
// children, not edit code).
func TestComposeSystemPromptSkipsWorkerTemplateForOrchestrator(t *testing.T) {
	prompt := composeSystemPrompt(Options{
		Mode:         ModeLongLived,
		Role:         "orchestrator",
		AgentProfile: "orchestrator",
		SystemPrompt: "[ORCHESTRATOR-FRAMING]",
	}, nil)
	assert.NotContains(t, prompt, "Worker — long-lived dispatch contract")
	assert.Contains(t, prompt, "[ORCHESTRATOR-FRAMING]")
}

// TestComposeSystemPromptSkipsWorkerTemplateForOneShot verifies ModeOneShot
// dispatches (planner, reviewer-end-agent, any bounded-mechanical
// kind=internal task) don't get the worker contract. Those callers run
// single bounded turns and their contracts come from their own templates;
// the worker contract's "commit + push + self-transition" steps would be
// nonsensical for a planner that doesn't edit code.
func TestComposeSystemPromptSkipsWorkerTemplateForOneShot(t *testing.T) {
	prompt := composeSystemPrompt(Options{
		Mode:         ModeOneShot,
		AgentProfile: "implementer",
		SystemPrompt: "[ONESHOT-FRAMING]",
	}, nil)
	assert.NotContains(t, prompt, "Worker — long-lived dispatch contract")
	assert.Contains(t, prompt, "[ONESHOT-FRAMING]")
}

// TestDefaultWorkerTemplate_Embed sanity-checks the embedded template
// resolved. Catches the "embed didn't fire" / "file moved" class of
// mistake at build time.
func TestDefaultWorkerTemplate_Embed(t *testing.T) {
	require.NotEmpty(t, scheduler.DefaultWorkerTemplate())
}
