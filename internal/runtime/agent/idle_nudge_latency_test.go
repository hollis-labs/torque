package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/substrate/harness/adapters/providertest"
	"github.com/hollis-labs/substrate/llm-core/contracts/runtimes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/steering"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
)

// A worker answering the idle reminder is working, not idle, even before
// its reply shows anything (CW-20261001-0117 review, finding 1). Torque
// learns a turn started only from its first parsed event, and a Claude turn
// can be silent for minutes first: the per-turn `system` init becomes an
// EventSessionID that go-agent-wrapper sends no envelope for, a thinking-only
// assistant message parses to nothing, and rate_limit_event is skipped. Here
// the fake claude, replaying go-providers' captured two-turn stream, answers
// the reminder with exactly that and stays silent for several windows before
// its text. The worker then moves its task to review. The run must end as
// that review, not be auto-routed mid-reply.
func TestRunLongLived_SlowReplyToReminderIsNotRouted(t *testing.T) {
	prevPoll, prevWindow := statusPollInterval, defaultIdleNudgeWindow
	statusPollInterval, defaultIdleNudgeWindow = 20*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { statusPollInterval, defaultIdleNudgeWindow = prevPoll, prevWindow })
	silence := 5 * defaultIdleNudgeWindow

	// The captured stream: turn one is steps 0-5 (user, system init,
	// thinking, text, rate_limit_event, result); turn two starts at the
	// second user message (step 6) with system init and a thinking-only
	// message (7, 8), then its text (9) and result (10).
	steps := providertest.FixtureSteps(t, "claude/stream_two_turns")
	require.Len(t, steps, 13, "the fixture's shape changed; re-derive the split")
	require.NotNil(t, steps[6].Recv, "step 6 is the second user message")
	script := append([]providertest.Step(nil), steps[:9]...)
	script = append(script, providertest.Sleep(silence))
	script = append(script, steps[9:]...)
	fake := providertest.New(t, runtimes.Claude, providertest.Script(script...))
	fake.Install()

	store := newTestStoreForLongLived(t)
	const taskID = "CW-TEST-LL-SLOW-REPLY"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: taskID, Title: "slow reply to the reminder", Status: "doing",
		Executor: "cli", Kind: "agent", AgentProfile: "test",
	}))
	deps := &Dependencies{
		Store:          store,
		StateWriter:    writeq.NewDirect(store),
		WorkspacesRoot: t.TempDir(),
		Profiles: config.ProfileMap{
			"test": {Executor: "cli", Provider: "claude-code", RuntimeKind: "streaming-stdio", PermissionMode: "acceptEdits"},
		},
		Reminder: steering.NewReminderRegistry(),
	}
	deps.Sessions = NewManager(deps)

	// The worker signals once its reply is visible: the second turn's text.
	var once sync.Once
	onEvent := func(ev executor.ExecutionEvent) {
		if strings.Contains(ev.Content, "Bye!") {
			once.Do(func() { _ = store.TransitionTask(taskID, "review") })
		}
	}

	done := make(chan *executor.ExecutionResult, 1)
	go func() {
		res, err := NewExecutor(deps).Run(context.Background(), &executor.ExecutionJob{
			TaskID: taskID, Kind: "agent", AgentProfile: "test", WorkingDir: t.TempDir(), RunID: 1,
		}, onEvent)
		assert.NoError(t, err)
		done <- res
	}()
	var res *executor.ExecutionResult
	select {
	case res = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the run did not end")
	}
	require.NotNil(t, res)

	comments, err := store.ListCommentsForEntity(sqlstore.EntityTypeTask, taskID)
	require.NoError(t, err)
	for _, c := range comments {
		assert.NotEqual(t, autoRouteCommentAuthor, c.Author, "auto-routed while the worker was answering the reminder: %s", c.Content)
	}
	assert.Equal(t, "review", res.Status, "reason: %s", res.Reason)
	assert.NotContains(t, res.Reason, "auto-routed")
	calls := fake.Calls()
	require.Len(t, calls, 1)
	reminded := false
	for _, line := range calls[0].Stdin {
		if strings.Contains(line, "You ended your turn without signalling") {
			reminded = true
		}
	}
	assert.True(t, reminded, "the second turn is the reminder; stdin: %q", calls[0].Stdin)
}
