package agent

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/steering"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
	"github.com/hollis-labs/torque/internal/testutil/testenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// claudeToolTurnFixture is a real `claude -p --output-format stream-json
// --verbose` turn (haiku, two Bash calls, then "done"), with the system
// events dropped and the session id zeroed. Its assistant events carry each
// message's usage with a placeholder output count (4+1+4); the `result`
// event carries the turn's real totals: 26 input, 286 output tokens.
const claudeToolTurnFixture = "testdata/claude_stream_json_tool_turn.jsonl"

// TestRunLongLived_DrainsTurnForClaudeUsage is CW-20261001-0042. A
// long-lived claude-code worker signals review with a tool call, so the
// turn is still open when the task leaves `doing`; claude writes the
// turn's `result`, the only line its usage is parsed from, one model call
// later. The fixture is replayed through go-providers' own claude parser:
// everything up to the second tool call's result, then the task moves to
// review, then the closing message and `result` after a model-call delay.
func TestRunLongLived_DrainsTurnForClaudeUsage(t *testing.T) {
	lines := readFixtureLines(t, claudeToolTurnFixture)
	// The fixture's line 7 is the second Bash call's tool_result: the point
	// a worker's review call would have returned.
	const transitionAfter = 8
	require.Greater(t, len(lines), transitionAfter)

	t.Run("waits for the turn's result", func(t *testing.T) {
		result, stops := runFixtureLongLived(t, lines, transitionAfter, 5*time.Second, 200*time.Millisecond)
		assert.Equal(t, "review", result.Status)
		assert.Equal(t, 26, result.Tokens.PromptTokens, "input tokens from the turn's result")
		assert.Equal(t, 286, result.Tokens.CompletionTokens, "output tokens from the turn's result, not the per-message placeholders")
		assert.Equal(t, int32(1), stops)
	})

	// Without the drain the session stops at the transition and the
	// `result` never arrives: the 0/0 the overnight smoke runs recorded.
	t.Run("without the grace the result is lost", func(t *testing.T) {
		result, _ := runFixtureLongLived(t, lines, transitionAfter, 0, time.Second)
		assert.Equal(t, 0, result.Tokens.PromptTokens)
		assert.Equal(t, 0, result.Tokens.CompletionTokens)
	})
}

func TestTurnTracker(t *testing.T) {
	t.Run("no turn open returns at once", func(t *testing.T) {
		var tr turnTracker
		assert.True(t, tr.waitEnd(time.Hour))
	})
	t.Run("done closes the open turn", func(t *testing.T) {
		var tr turnTracker
		tr.observe(llmtypes.StreamEvent{Type: llmtypes.EventToolUse})
		tr.observe(llmtypes.StreamEvent{Type: llmtypes.EventUsage})
		go tr.observe(llmtypes.StreamEvent{Type: llmtypes.EventDone})
		assert.True(t, tr.waitEnd(5*time.Second))
	})
	t.Run("error closes the open turn", func(t *testing.T) {
		var tr turnTracker
		tr.observe(llmtypes.StreamEvent{Type: llmtypes.EventDelta})
		tr.observe(llmtypes.StreamEvent{Type: llmtypes.EventError})
		assert.True(t, tr.waitEnd(time.Hour))
	})
	t.Run("an open turn times out", func(t *testing.T) {
		var tr turnTracker
		tr.observe(llmtypes.StreamEvent{Type: llmtypes.EventDelta})
		assert.False(t, tr.waitEnd(20*time.Millisecond))
	})
	t.Run("a new turn reopens after done", func(t *testing.T) {
		var tr turnTracker
		tr.observe(llmtypes.StreamEvent{Type: llmtypes.EventDelta})
		tr.observe(llmtypes.StreamEvent{Type: llmtypes.EventDone})
		tr.observe(llmtypes.StreamEvent{Type: llmtypes.EventDelta})
		assert.False(t, tr.waitEnd(20*time.Millisecond))
	})
}

func readFixtureLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(filepath.FromSlash(path))
	require.NoError(t, err)
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	require.NoError(t, sc.Err())
	return lines
}

// runFixtureLongLived runs a long-lived claude-code dispatch whose session
// replays lines through the real claude parser: lines[:split], then the
// task moves to review, then lines[split:] after delay. It returns the
// run's result and how many times the session was stopped.
func runFixtureLongLived(t *testing.T, lines []string, split int, grace, delay time.Duration) (*executor.ExecutionResult, int32) {
	t.Helper()
	prevPoll, prevGrace := statusPollInterval, turnDrainGrace
	statusPollInterval, turnDrainGrace = 25*time.Millisecond, grace
	t.Cleanup(func() { statusPollInterval, turnDrainGrace = prevPoll, prevGrace })

	store := newTestStoreForLongLived(t)
	const taskID = "CW-TEST-LL-USAGE"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: taskID, Title: "claude usage test", Status: "doing",
		Executor: "cli", Kind: "agent", AgentProfile: "test",
	}))

	parse := provider.NewClaudeAdapterStreamingStdio().ParseLine
	replay := func(s *replaySession) {
		for i, line := range lines {
			if i == split {
				if err := store.TransitionTask(taskID, "review"); err != nil {
					t.Errorf("transition to review: %v", err)
					return
				}
				time.Sleep(delay)
			}
			evs, err := parse([]byte(line))
			if err != nil {
				t.Errorf("parse fixture line %d: %v", i, err)
				return
			}
			for _, ev := range evs {
				if !s.send(ev) {
					return
				}
			}
		}
	}
	fr := &replayRuntime{replay: replay}
	deps := &Dependencies{
		WorkspacesRoot: testenv.WorkspacesRoot(t),
		Store:          store,
		StateWriter:    writeq.NewDirect(store),
		Profiles: config.ProfileMap{
			"test": {Executor: "cli", Provider: "claude-code", RuntimeKind: "streaming-stdio"},
		},
		RuntimeFactory: func(agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error) {
			return fr, nil
		},
		Reminder: steering.NewReminderRegistry(),
	}
	deps.Sessions = NewManager(deps).WithIDFunc(func() string { return "SES-USAGE" })

	result, err := NewExecutor(deps).Run(context.Background(), &executor.ExecutionJob{
		TaskID: taskID, Kind: "agent", AgentProfile: "test", WorkingDir: t.TempDir(), RunID: 1,
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	fr.wg.Wait()
	return result, fr.stopCount.Load()
}

// replayRuntime starts one replaySession, which plays the test's replay
// func into the session's event fanout once the first turn is fired.
type replayRuntime struct {
	replay    func(*replaySession)
	stopCount atomic.Int32
	wg        sync.WaitGroup
}

func (r *replayRuntime) ID() string   { return "replay-fake" }
func (r *replayRuntime) Kind() string { return string(RuntimeKindStreamingStdio) }
func (r *replayRuntime) Caps() agentsessions.Capabilities {
	return agentsessions.Capabilities{}
}
func (r *replayRuntime) Prepare(context.Context) error { return nil }
func (r *replayRuntime) Start(_ context.Context, opts agentsessions.StartOptions) (agentsessions.Session, error) {
	s := &replaySession{done: make(chan struct{}), events: opts.EventFanout, stopCount: &r.stopCount}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.replay(s)
	}()
	return s, nil
}

type replaySession struct {
	mu        sync.Mutex
	stopped   bool
	done      chan struct{}
	events    chan<- llmtypes.StreamEvent
	stopCount *atomic.Int32
}

// send delivers ev unless the session was stopped, as a killed claude
// process writes nothing more. Holding mu across the send keeps it ahead
// of Stop, and so of the fanout close that follows Stop.
func (s *replaySession) send(ev llmtypes.StreamEvent) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.events == nil {
		return false
	}
	select {
	case s.events <- ev:
		return true
	case <-time.After(5 * time.Second):
		return false
	}
}

func (s *replaySession) Wait() (int, error) {
	<-s.done
	return 0, nil
}

func (s *replaySession) Stop(context.Context) error {
	s.stopCount.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		close(s.done)
	}
	return nil
}

func (s *replaySession) SendInput(context.Context, []byte) error      { return nil }
func (s *replaySession) Resize(context.Context, uint16, uint16) error { return nil }
func (s *replaySession) Health() agentsessions.HealthStatus {
	return agentsessions.HealthStatus{Alive: true, PID: 4321}
}
func (s *replaySession) CheckpointHints() (agentsessions.CheckpointHint, bool) {
	return agentsessions.CheckpointHint{}, false
}
