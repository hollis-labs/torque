package scheduler

import (
	"sync"
	"time"
)

// progressThrottler rate-limits run.progress emissions for high-frequency
// event classes (currently: tokens). It keeps a per-run timestamp of the last
// allowed emission and rejects subsequent calls that arrive inside the window.
//
// Tokens events can flood the SSE bus on chatty LLM runs — throttling at the
// publish site keeps the browser's event stream readable without losing the
// low-frequency signal types (notes, artifacts) which pass through untouched.
type progressThrottler struct {
	mu     sync.Mutex
	last   map[int64]time.Time
	window time.Duration
}

func newProgressThrottler(window time.Duration) *progressThrottler {
	return &progressThrottler{
		last:   make(map[int64]time.Time),
		window: window,
	}
}

// allow reports whether a throttled emission for runID may be sent at now. If
// it returns true it has also recorded the emission so subsequent calls inside
// the window are rejected.
func (p *progressThrottler) allow(runID int64, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if t, ok := p.last[runID]; ok && now.Sub(t) < p.window {
		return false
	}
	p.last[runID] = now
	return true
}

// release drops tracked state for runID. Call when a run finishes so the map
// doesn't grow unbounded over long-lived schedulers.
func (p *progressThrottler) release(runID int64) {
	p.mu.Lock()
	delete(p.last, runID)
	p.mu.Unlock()
}
