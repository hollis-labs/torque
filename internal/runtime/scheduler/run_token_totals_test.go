package scheduler

import (
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunTokenTotalsSumsDeltasPerRun(t *testing.T) {
	r := newRunTokenTotals()
	r.add(1, executor.TokenUsage{PromptTokens: 10, CompletionTokens: 2, CacheReadTokens: 100, CacheWriteTokens: 5, Cost: 0.01})
	got := r.add(1, executor.TokenUsage{PromptTokens: 3, CompletionTokens: 4, CacheReadTokens: 50, Cost: 0.02})
	assert.Equal(t, executor.TokenUsage{PromptTokens: 13, CompletionTokens: 6, CacheReadTokens: 150, CacheWriteTokens: 5, Cost: 0.03}, roundCost(got))

	other := r.add(2, executor.TokenUsage{PromptTokens: 1})
	assert.Equal(t, 1, other.PromptTokens, "runs are tracked independently")

	r.release(1)
	assert.Equal(t, 1, r.size())
	assert.Equal(t, 7, r.add(1, executor.TokenUsage{PromptTokens: 7}).PromptTokens, "a released run starts from zero")
}

func TestRunTokenTotalsConcurrentAdds(t *testing.T) {
	r := newRunTokenTotals()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.add(9, executor.TokenUsage{CompletionTokens: 1})
		}()
	}
	wg.Wait()
	assert.Equal(t, 50, r.add(9, executor.TokenUsage{}).CompletionTokens)
}

// TestPublishProgressTotalsSurviveThrottle is the multi-step case: several
// usage deltas land inside one throttle window, only the first is emitted,
// and the next emission's totals still include every one of them. The
// deltas are modeled on an OpenCode two-tool turn (three step_finish
// events); the per-event fields keep their delta meaning.
func TestPublishProgressTotalsSurviveThrottle(t *testing.T) {
	s := &Scheduler{
		bus:               NewEventBus(),
		progressThrottler: newProgressThrottler(time.Hour),
		tokenTotals:       newRunTokenTotals(),
	}
	sub := s.bus.Subscribe()
	defer s.bus.Unsubscribe(sub)
	const runID = 77

	steps := []executor.TokenUsage{
		{PromptTokens: 11200, CompletionTokens: 61, CacheReadTokens: 0, CacheWriteTokens: 11000},
		{PromptTokens: 180, CompletionTokens: 48, CacheReadTokens: 11000, CacheWriteTokens: 200},
		{PromptTokens: 90, CompletionTokens: 4, CacheReadTokens: 11200, CacheWriteTokens: 0},
	}
	for _, u := range steps {
		u := u
		s.publishProgress("CW-T", runID, executor.ExecutionEvent{Type: executor.EventTokenUsage, Tokens: &u})
	}
	first := recvTokens(t, sub)
	assert.EqualValues(t, 11200, first["prompt"])
	assertNoEvent(t, sub, "steps 2-3 are inside the throttle window")

	// Window elapses; one more (empty) usage update goes out.
	s.progressThrottler.release(runID)
	s.publishProgress("CW-T", runID, executor.ExecutionEvent{Type: executor.EventTokenUsage, Tokens: &executor.TokenUsage{CompletionTokens: 1}})
	next := recvTokens(t, sub)

	assert.EqualValues(t, 1, next["completion"], "per-event completion stays the delta")
	totals := next["totals"].(map[string]interface{})
	assert.EqualValues(t, 11200+180+90, totals["prompt"])
	assert.EqualValues(t, 61+48+4+1, totals["completion"])
	assert.EqualValues(t, 0+11000+11200, totals["cache_read"])
	assert.EqualValues(t, 11000+200, totals["cache_write"])
	_, hasTotal := totals["total"]
	assert.False(t, hasTotal, "no summed context-size field is ever published")
}

// A single usage event per turn (claude, codex) is unchanged: totals equal
// the one delta.
func TestPublishProgressSingleEventTotalsEqualDelta(t *testing.T) {
	s := &Scheduler{
		bus:               NewEventBus(),
		progressThrottler: newProgressThrottler(time.Hour),
		tokenTotals:       newRunTokenTotals(),
	}
	sub := s.bus.Subscribe()
	defer s.bus.Unsubscribe(sub)

	s.publishProgress("CW-T", 5, executor.TokenEvent(1200, 340, 0))
	got := recvTokens(t, sub)
	assert.EqualValues(t, 1200, got["prompt"])
	assert.EqualValues(t, 340, got["completion"])
	totals := got["totals"].(map[string]interface{})
	assert.EqualValues(t, 1200, totals["prompt"])
	assert.EqualValues(t, 340, totals["completion"])
	assert.EqualValues(t, 0, totals["cost"])
}

func recvTokens(t *testing.T, sub <-chan SchedulerEvent) map[string]interface{} {
	t.Helper()
	select {
	case e := <-sub:
		require.Equal(t, "run.progress", e.Type)
		data := e.Data.(map[string]interface{})
		require.Equal(t, "tokens", data["kind"])
		return data
	case <-time.After(time.Second):
		t.Fatal("expected a tokens run.progress event")
		return nil
	}
}

func assertNoEvent(t *testing.T, sub <-chan SchedulerEvent, msg string) {
	t.Helper()
	select {
	case e := <-sub:
		t.Fatalf("%s: unexpected event %+v", msg, e)
	case <-time.After(50 * time.Millisecond):
	}
}

func roundCost(u executor.TokenUsage) executor.TokenUsage {
	u.Cost = float64(int(u.Cost*1000+0.5)) / 1000
	return u
}
