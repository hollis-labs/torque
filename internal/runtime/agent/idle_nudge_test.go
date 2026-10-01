package agent

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/runtime/steering"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// endedTurn returns a tracker whose last turn ended at end.
func endedTurn(end time.Time) *turnTracker {
	return &turnTracker{lastEnd: end}
}

func TestIdleNudger(t *testing.T) {
	const window = time.Minute
	t0 := time.Date(2026, 10, 1, 5, 0, 0, 0, time.UTC)
	newNudger := func(turn *turnTracker) (*idleNudger, *int) {
		sends := 0
		return &idleNudger{window: window, turn: turn, nudge: func() error { sends++; return nil }}, &sends
	}

	t.Run("reminds once after the window, then routes a window later", func(t *testing.T) {
		n, sends := newNudger(endedTurn(t0))
		assert.False(t, n.step(t0.Add(window-time.Second)), "inside the window")
		assert.Equal(t, 0, *sends)
		assert.False(t, n.step(t0.Add(window)), "the reminder goes out; no route yet")
		assert.Equal(t, 1, *sends)
		assert.False(t, n.step(t0.Add(2*window-time.Second)), "inside the window after the reminder")
		assert.True(t, n.step(t0.Add(2*window)), "still idle a window after the reminder")
		assert.Equal(t, 1, *sends, "one reminder per run")
	})

	t.Run("a reply to the reminder restarts the second window", func(t *testing.T) {
		turn := endedTurn(t0)
		n, _ := newNudger(turn)
		require.False(t, n.step(t0.Add(window)))
		turn.lastEnd = t0.Add(window + 30*time.Second) // the worker answered and ended that turn
		assert.False(t, n.step(t0.Add(2*window)))
		assert.True(t, n.step(t0.Add(2*window+30*time.Second)))
	})

	t.Run("never while a turn is in flight", func(t *testing.T) {
		turn := endedTurn(t0)
		turn.inFlight = true
		n, sends := newNudger(turn)
		assert.False(t, n.step(t0.Add(10*window)))
		assert.Equal(t, 0, *sends)
	})

	t.Run("never before a turn has ended", func(t *testing.T) {
		n, sends := newNudger(&turnTracker{})
		assert.False(t, n.step(t0.Add(10*window)))
		assert.Equal(t, 0, *sends)
	})

	t.Run("never while the task has a pending checkpoint", func(t *testing.T) {
		n, sends := newNudger(endedTurn(t0))
		n.waiting = func() bool { return true }
		assert.False(t, n.step(t0.Add(10*window)))
		assert.Equal(t, 0, *sends)
		n.sentAt = t0 // a checkpoint opened after the reminder still holds the route
		assert.False(t, n.step(t0.Add(10*window)))
	})

	t.Run("a failed send is retried, and counts only once sent", func(t *testing.T) {
		fail := true
		sends := 0
		n := &idleNudger{window: window, turn: endedTurn(t0), nudge: func() error {
			if fail {
				fail = false
				return errors.New("pipe closed")
			}
			sends++
			return nil
		}}
		assert.False(t, n.step(t0.Add(window)))
		assert.True(t, n.sentAt.IsZero(), "a failed send is not a reminder")
		assert.False(t, n.step(t0.Add(window+5*time.Second)))
		assert.Equal(t, 1, sends)
		assert.True(t, n.step(t0.Add(2*window+5*time.Second)))
	})

	t.Run("a zero window or nil nudger never acts", func(t *testing.T) {
		n, sends := newNudger(endedTurn(t0))
		n.window = 0
		assert.False(t, n.step(t0.Add(time.Hour)))
		assert.Equal(t, 0, *sends)
		var none *idleNudger
		assert.False(t, none.step(t0.Add(time.Hour)))
	})
}

func TestResolveIdleNudgeWindow(t *testing.T) {
	agentTask := func(md map[string]any) Options { return Options{TaskKind: "agent", Metadata: md} }
	assert.Equal(t, defaultIdleNudgeWindow, resolveIdleNudgeWindow(agentTask(nil)))
	assert.Equal(t, 45*time.Second, resolveIdleNudgeWindow(agentTask(map[string]any{"idle_nudge_seconds": 45})))
	assert.Equal(t, time.Duration(0), resolveIdleNudgeWindow(agentTask(map[string]any{"idle_nudge_seconds": 0})), "0 turns it off")
	assert.Equal(t, defaultIdleNudgeWindow, resolveIdleNudgeWindow(agentTask(map[string]any{"idle_nudge_seconds": idleNudgeMaxSeconds + 1})), "out of range falls back")
	// Only kind=agent worker tasks: a plan, parent or internal task's
	// session waits between turns by design.
	for _, kind := range []string{"plan", "parent", "internal", ""} {
		assert.Equal(t, time.Duration(0), resolveIdleNudgeWindow(Options{TaskKind: kind}), "kind %q", kind)
	}
}

// An idle worker that is waiting on purpose is neither reminded nor routed.
func TestWorkerWaitsByDesign(t *testing.T) {
	const taskID, sessID = "CW-TEST-WAITS", "SES-WAITS"
	setup := func(t *testing.T) (*Dependencies, *sqlstore.Store) {
		store := newTestStoreForLongLived(t)
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: taskID, Title: "parent", Status: "doing", Executor: "cli", Kind: "agent", AgentProfile: "test"}))
		return &Dependencies{Store: store, Reminder: steering.NewReminderRegistry(), Polls: steering.NewPollRegistry(time.Minute)}, store
	}

	t.Run("nothing to wait on", func(t *testing.T) {
		deps, _ := setup(t)
		assert.False(t, workerWaitsByDesign(deps, taskID, sessID))
	})
	t.Run("an open child task", func(t *testing.T) {
		deps, store := setup(t)
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-TEST-WAITS-C1", Title: "child", Status: "doing", Executor: "cli", Kind: "agent", AgentProfile: "test",
			ParentID: sql.NullString{String: taskID, Valid: true}}))
		assert.True(t, workerWaitsByDesign(deps, taskID, sessID))
		require.NoError(t, store.TransitionTask("CW-TEST-WAITS-C1", "done"))
		assert.False(t, workerWaitsByDesign(deps, taskID, sessID), "a finished child is not waited on")
	})
	t.Run("a child in review is still open", func(t *testing.T) {
		deps, store := setup(t)
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-TEST-WAITS-C2", Title: "child", Status: "review", Executor: "cli", Kind: "agent", AgentProfile: "test",
			ParentID: sql.NullString{String: taskID, Valid: true}}))
		assert.True(t, workerWaitsByDesign(deps, taskID, sessID))
	})
	t.Run("an undismissed steering envelope", func(t *testing.T) {
		deps, _ := setup(t)
		deps.Reminder.RecordDelivery(taskID, reminderTestEnvelope("ENV-1", `{"text":"wait for me"}`))
		assert.True(t, workerWaitsByDesign(deps, taskID, sessID))
		deps.Reminder.Dismiss(taskID, "ENV-1")
		assert.False(t, workerWaitsByDesign(deps, taskID, sessID), "a dismissed envelope is not waited on")
	})
	t.Run("the session polls its inbox", func(t *testing.T) {
		deps, _ := setup(t)
		deps.Polls.MarkPolling(gomsg.Address{Kind: gomsg.KindSession, Authority: "local", ID: sessID}.URN())
		assert.True(t, workerWaitsByDesign(deps, taskID, sessID))
	})
	t.Run("the task's agent address polls its inbox", func(t *testing.T) {
		deps, _ := setup(t)
		deps.Polls.MarkPolling(gomsg.Address{Kind: gomsg.KindAgent, Authority: "torque", ID: taskID}.URN())
		assert.True(t, workerWaitsByDesign(deps, taskID, sessID))
	})
}

func TestRouteUnsignalled(t *testing.T) {
	status, reason := routeUnsignalled(scheduler.WorkerVerdict{Kind: scheduler.VerdictPassed, CommitCount: 2}, 0)
	assert.Equal(t, "review", status)
	assert.Contains(t, reason, "auto-routed")
	assert.Contains(t, reason, "2 commit(s)")

	status, _ = routeUnsignalled(scheduler.WorkerVerdict{Kind: scheduler.VerdictPassedNoEditsExpected}, 1)
	assert.Equal(t, "review", status, "a comment or artifact on the task is output")

	status, reason = routeUnsignalled(scheduler.WorkerVerdict{Kind: scheduler.VerdictPassedNoEditsExpected, ToolUseHistogram: map[string]int{"Read": 3}}, 0)
	assert.Equal(t, "blocked", status, "tool calls alone are not output")
	assert.Contains(t, reason, "auto-routed")

	status, reason = routeUnsignalled(scheduler.WorkerVerdict{Kind: scheduler.VerdictFailedNoCommitsWithEdits, Reason: "edits preserved in worktree /w"}, 1)
	assert.Equal(t, "blocked", status)
	assert.Contains(t, reason, "edits preserved in worktree /w")
}

// A long-lived worker that ends its turn without moving its task out of
// doing gets one reminder, then is routed on what it left behind
// (CW-20261001-0117). The fake worker plays one turn ending in done and
// never signals; how it answers the reminder varies per case.
func TestRunLongLived_UnsignalledWorker(t *testing.T) {
	cases := []struct {
		name string
		// comment posts a task comment during the first turn.
		comment bool
		// review moves the task to review in answer to the reminder.
		review bool
		// openChild gives the task a child still in doing; the worker
		// moves the task to review itself after a few idle windows.
		openChild  bool
		wantStatus string
	}{
		{name: "answers the reminder by signalling", review: true, wantStatus: "review"},
		{name: "stays idle with output on the task", comment: true, wantStatus: "review"},
		{name: "stays idle with nothing to show", wantStatus: "blocked"},
		// An orchestrating worker waiting on a child it promoted is left
		// alone; the run ends only when the worker signals.
		{name: "waits on an open child task", openChild: true, wantStatus: "review"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prevPoll, prevWindow := statusPollInterval, defaultIdleNudgeWindow
			statusPollInterval, defaultIdleNudgeWindow = 20*time.Millisecond, 150*time.Millisecond
			t.Cleanup(func() { statusPollInterval, defaultIdleNudgeWindow = prevPoll, prevWindow })

			store := newTestStoreForLongLived(t)
			const taskID = "CW-TEST-LL-NUDGE"
			require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
				ID: taskID, Title: "unsignalled worker", Status: "doing",
				Executor: "cli", Kind: "agent", AgentProfile: "test",
			}))

			if tc.openChild {
				require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
					ID: taskID + "-CHILD", Title: "promoted child", Status: "doing",
					Executor: "cli", Kind: "agent", AgentProfile: "test",
					ParentID: sql.NullString{String: taskID, Valid: true},
				}))
				go func() {
					time.Sleep(5 * defaultIdleNudgeWindow)
					_ = store.TransitionTask(taskID, "review")
				}()
			}
			fr := &nudgeRuntime{
				firstTurn: func() {
					if tc.comment {
						require.NoError(t, store.AddComment(&sqlstore.CommentRecord{EntityType: "task", EntityID: taskID, Author: "worker", Content: "Summary posted. Task complete."}))
					}
				},
				onInput: func(s *nudgeSession) {
					if tc.review {
						_ = store.TransitionTask(taskID, "review")
					}
					s.playTurn("ok")
				},
			}
			deps := &Dependencies{
				Store:       store,
				StateWriter: writeq.NewDirect(store),
				Profiles: config.ProfileMap{
					"test": {Executor: "cli", Provider: "claude-code", RuntimeKind: "streaming-stdio"},
				},
				RuntimeFactory: func(agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error) { return fr, nil },
				Reminder:       steering.NewReminderRegistry(),
			}
			deps.Sessions = NewManager(deps).WithIDFunc(func() string { return "SES-NUDGE" })

			done := make(chan *executor.ExecutionResult, 1)
			go func() {
				res, err := NewExecutor(deps).Run(context.Background(), &executor.ExecutionJob{
					TaskID: taskID, Kind: "agent", AgentProfile: "test", WorkingDir: t.TempDir(), RunID: 1,
				}, nil)
				assert.NoError(t, err)
				done <- res
			}()
			var res *executor.ExecutionResult
			select {
			case res = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the run did not end; an unsignalled worker should be reminded and then routed")
			}
			require.NotNil(t, res)

			assert.Equal(t, tc.wantStatus, res.Status, "reason: %s", res.Reason)
			inputs := fr.inputs()
			reminders := 0
			for _, in := range inputs {
				if strings.Contains(in, "You ended your turn without signalling") {
					reminders++
				}
			}
			if tc.openChild {
				assert.Zero(t, reminders, "a worker waiting on its child is not reminded: %q", inputs)
			} else {
				assert.Equal(t, 1, reminders, "exactly one reminder: %q", inputs)
			}

			comments, err := store.ListCommentsForEntity(sqlstore.EntityTypeTask, taskID)
			require.NoError(t, err)
			var routed []string
			for _, c := range comments {
				if c.Author == autoRouteCommentAuthor {
					routed = append(routed, c.Content)
				}
			}
			if tc.review || tc.openChild {
				assert.Empty(t, res.Reason)
				assert.Empty(t, routed, "a worker that signalled was not auto-routed")
				return
			}
			assert.Contains(t, res.Reason, "auto-routed")
			require.Len(t, routed, 1, "the route is recorded on the task")
			assert.Contains(t, routed[0], "Moved to "+tc.wantStatus+" by Torque")
		})
	}
}

// nudgeRuntime starts one nudgeSession: a streaming worker that plays one
// turn ending in done once started, and records each input it is sent.
type nudgeRuntime struct {
	firstTurn func()
	onInput   func(*nudgeSession)
	mu        sync.Mutex
	sess      *nudgeSession
}

func (r *nudgeRuntime) ID() string                       { return "nudge-fake" }
func (r *nudgeRuntime) Kind() string                     { return string(RuntimeKindStreamingStdio) }
func (r *nudgeRuntime) Caps() agentsessions.Capabilities { return agentsessions.Capabilities{} }
func (r *nudgeRuntime) Prepare(context.Context) error    { return nil }
func (r *nudgeRuntime) Start(_ context.Context, opts agentsessions.StartOptions) (agentsessions.Session, error) {
	s := &nudgeSession{done: make(chan struct{}), events: opts.EventFanout, onInput: r.onInput}
	r.mu.Lock()
	r.sess = s
	r.mu.Unlock()
	go func() {
		if r.firstTurn != nil {
			r.firstTurn()
		}
		s.playTurn("Summary posted successfully. Task complete.")
	}()
	return s, nil
}

func (r *nudgeRuntime) inputs() []string {
	r.mu.Lock()
	s := r.sess
	r.mu.Unlock()
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.inputs...)
}

type nudgeSession struct {
	mu      sync.Mutex
	stopped bool
	done    chan struct{}
	events  chan<- llmtypes.StreamEvent
	inputs  []string
	onInput func(*nudgeSession)
}

// playTurn emits one turn: text, then done.
func (s *nudgeSession) playTurn(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.events == nil {
		return
	}
	for _, ev := range []llmtypes.StreamEvent{{Type: llmtypes.EventDelta, Content: text}, {Type: llmtypes.EventDone}} {
		select {
		case s.events <- ev:
		case <-time.After(5 * time.Second):
			return
		}
	}
}

func (s *nudgeSession) SendInput(_ context.Context, data []byte) error {
	s.mu.Lock()
	s.inputs = append(s.inputs, string(data))
	s.mu.Unlock()
	if s.onInput != nil {
		go s.onInput(s)
	}
	return nil
}

func (s *nudgeSession) Wait() (int, error) {
	<-s.done
	return 0, nil
}

func (s *nudgeSession) Stop(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.stopped {
		s.stopped = true
		close(s.done)
	}
	return nil
}

func (s *nudgeSession) Resize(context.Context, uint16, uint16) error { return nil }
func (s *nudgeSession) Health() agentsessions.HealthStatus {
	return agentsessions.HealthStatus{Alive: true, PID: 4322}
}
func (s *nudgeSession) CheckpointHints() (agentsessions.CheckpointHint, bool) {
	return agentsessions.CheckpointHint{}, false
}
