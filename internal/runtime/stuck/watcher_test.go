package stuck_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/runtime/stuck"
)

// --- watcher fakes ----------------------------------------------------

type fakeLister struct {
	mu       sync.Mutex
	sessions []stuck.LiveSession
	err      error
	calls    int
}

func (f *fakeLister) RunningSessions() ([]stuck.LiveSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return append([]stuck.LiveSession(nil), f.sessions...), nil
}

func (f *fakeLister) set(sessions ...stuck.LiveSession) {
	f.mu.Lock()
	f.sessions = sessions
	f.mu.Unlock()
}

// recordingProbe captures every ProbeInput the Watcher fires and lets a
// test gate how long a probe "runs" so the in-flight de-dup is observable.
type recordingProbe struct {
	mu      sync.Mutex
	inputs  []stuck.ProbeInput
	fired   chan stuck.ProbeInput
	release chan struct{} // probes block on this until closed (nil = return immediately)
}

func newRecordingProbe() *recordingProbe {
	return &recordingProbe{fired: make(chan stuck.ProbeInput, 64)}
}

func (r *recordingProbe) fn(ctx context.Context, in stuck.ProbeInput) stuck.Result {
	r.mu.Lock()
	r.inputs = append(r.inputs, in)
	rel := r.release
	r.mu.Unlock()
	r.fired <- in
	if rel != nil {
		select {
		case <-rel:
		case <-ctx.Done():
		}
	}
	return stuck.Result{Outcome: stuck.OutcomeResumed, TaskID: in.TaskID, OriginalSessionID: in.SessionID}
}

func (r *recordingProbe) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.inputs)
}

// newWatcherDeps builds a WatcherDeps with a fake lister and the probe
// surfaces stubbed (the recordingProbe replaces Probe wholesale, so the
// surfaces are never exercised — they only need to be non-nil to pass
// New's required-deps check).
func newWatcherDeps(lister stuck.SessionLister) stuck.WatcherDeps {
	return stuck.WatcherDeps{
		Lister:     lister,
		Sender:     newFakeSender(),
		Source:     newFakeSource(),
		Dispatch:   &fakeDispatcher{},
		Resume:     &fakeResume{},
		Checkpoint: &fakeCheckpoint{},
	}
}

// --- construction -----------------------------------------------------

func TestNewWatcher_NilDep_Errors(t *testing.T) {
	full := newWatcherDeps(&fakeLister{})

	cases := []struct {
		name   string
		mutate func(*stuck.WatcherDeps)
		want   string
	}{
		{"lister", func(d *stuck.WatcherDeps) { d.Lister = nil }, "Lister"},
		{"sender", func(d *stuck.WatcherDeps) { d.Sender = nil }, "Sender"},
		{"source", func(d *stuck.WatcherDeps) { d.Source = nil }, "Source"},
		{"dispatch", func(d *stuck.WatcherDeps) { d.Dispatch = nil }, "Dispatch"},
		{"resume", func(d *stuck.WatcherDeps) { d.Resume = nil }, "Resume"},
		{"checkpoint", func(d *stuck.WatcherDeps) { d.Checkpoint = nil }, "Checkpoint"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deps := full
			c.mutate(&deps)
			w, err := stuck.New(deps, stuck.WatcherConfig{})
			assert.Nil(t, w)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

func TestNewWatcher_ConfigDefaultsApply(t *testing.T) {
	w, err := stuck.New(newWatcherDeps(&fakeLister{}), stuck.WatcherConfig{})
	require.NoError(t, err)
	require.NotNil(t, w)
	// Defaults are internal; exercising Start/Close proves the zero-config
	// Watcher is usable (a zero ScanInterval would panic time.NewTicker).
	w.Start(context.Background())
	w.Close()
}

// --- stuck detection --------------------------------------------------

func TestWatcher_ProbesStuckSession(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{}
	lister.set(stuck.LiveSession{
		SessionID:    "SESS-stuck",
		TaskID:       "T-stuck",
		LastActivity: now.Add(-20 * time.Minute), // well past threshold
	})

	rp := newRecordingProbe()
	w, err := stuck.New(newWatcherDeps(lister), stuck.WatcherConfig{
		IdleThreshold: 10 * time.Minute,
		ScanInterval:  time.Hour, // only the immediate first scan matters
	})
	require.NoError(t, err)
	w.WithProbeFunc(rp.fn).WithNowFunc(func() time.Time { return now })

	w.Start(context.Background())
	defer w.Close()

	select {
	case in := <-rp.fired:
		assert.Equal(t, "SESS-stuck", in.SessionID)
		assert.Equal(t, "T-stuck", in.TaskID)
	case <-time.After(time.Second):
		t.Fatal("watcher did not probe the stuck session")
	}
}

func TestWatcher_SkipsActiveSession(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{}
	lister.set(stuck.LiveSession{
		SessionID:    "SESS-active",
		TaskID:       "T-active",
		LastActivity: now.Add(-1 * time.Minute), // recent — not stuck
	})

	rp := newRecordingProbe()
	w, err := stuck.New(newWatcherDeps(lister), stuck.WatcherConfig{
		IdleThreshold: 10 * time.Minute,
		ScanInterval:  time.Hour,
	})
	require.NoError(t, err)
	w.WithProbeFunc(rp.fn).WithNowFunc(func() time.Time { return now })

	w.Start(context.Background())
	defer w.Close()

	select {
	case <-rp.fired:
		t.Fatal("watcher probed a session that was still active")
	case <-time.After(150 * time.Millisecond):
		// expected — no probe
	}
	assert.Equal(t, 0, rp.count())
}

func TestWatcher_SkipsSessionWithoutTaskID(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{}
	lister.set(stuck.LiveSession{
		SessionID:    "SESS-notask",
		TaskID:       "", // Probe requires a TaskID — skip rather than fire a doomed probe
		LastActivity: now.Add(-30 * time.Minute),
	})

	rp := newRecordingProbe()
	w, err := stuck.New(newWatcherDeps(lister), stuck.WatcherConfig{
		IdleThreshold: 10 * time.Minute,
		ScanInterval:  time.Hour,
	})
	require.NoError(t, err)
	w.WithProbeFunc(rp.fn).WithNowFunc(func() time.Time { return now })

	w.Start(context.Background())
	defer w.Close()

	select {
	case <-rp.fired:
		t.Fatal("watcher probed a session with no task id")
	case <-time.After(150 * time.Millisecond):
	}
}

// --- de-dup -----------------------------------------------------------

// A probe that outlives the scan interval must not be stacked: while one
// probe for a session is in flight, later scans skip that session.
func TestWatcher_InFlightDedup(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{}
	lister.set(stuck.LiveSession{
		SessionID:    "SESS-1",
		TaskID:       "T-1",
		LastActivity: now.Add(-30 * time.Minute),
	})

	rp := newRecordingProbe()
	rp.release = make(chan struct{}) // probes block until released

	w, err := stuck.New(newWatcherDeps(lister), stuck.WatcherConfig{
		IdleThreshold: 10 * time.Minute,
		ScanInterval:  20 * time.Millisecond, // many scans while the probe is blocked
	})
	require.NoError(t, err)
	w.WithProbeFunc(rp.fn).WithNowFunc(func() time.Time { return now })

	w.Start(context.Background())
	defer func() {
		close(rp.release)
		w.Close()
	}()

	// First probe fires.
	select {
	case <-rp.fired:
	case <-time.After(time.Second):
		t.Fatal("first probe never fired")
	}

	// Several scan intervals pass while the probe is still blocked — no
	// second probe for the same session may be launched.
	time.Sleep(120 * time.Millisecond)
	assert.Equal(t, 1, rp.count(), "in-flight probe was re-triggered")
}

// Once a probe completes, the session becomes eligible again — the next
// scan re-probes it if it is still stuck.
func TestWatcher_ReprobesAfterCompletion(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	lister := &fakeLister{}
	lister.set(stuck.LiveSession{
		SessionID:    "SESS-1",
		TaskID:       "T-1",
		LastActivity: now.Add(-30 * time.Minute),
	})

	rp := newRecordingProbe() // release nil — probes return immediately

	w, err := stuck.New(newWatcherDeps(lister), stuck.WatcherConfig{
		IdleThreshold: 10 * time.Minute,
		ScanInterval:  20 * time.Millisecond,
	})
	require.NoError(t, err)
	w.WithProbeFunc(rp.fn).WithNowFunc(func() time.Time { return now })

	w.Start(context.Background())
	defer w.Close()

	// Drain at least two probes — proves the in-flight claim is released
	// when a probe finishes.
	for i := 0; i < 2; i++ {
		select {
		case <-rp.fired:
		case <-time.After(time.Second):
			t.Fatalf("expected re-probe %d after completion", i+1)
		}
	}
}

// --- lister errors ----------------------------------------------------

func TestWatcher_ListerError_DoesNotHaltLoop(t *testing.T) {
	lister := &fakeLister{err: errors.New("db locked")}

	rp := newRecordingProbe()
	w, err := stuck.New(newWatcherDeps(lister), stuck.WatcherConfig{
		IdleThreshold: 10 * time.Minute,
		ScanInterval:  20 * time.Millisecond,
	})
	require.NoError(t, err)
	w.WithProbeFunc(rp.fn)

	w.Start(context.Background())
	defer w.Close()

	// The loop keeps ticking despite the lister error — observe several
	// scan attempts rather than a single one then silence.
	require.Eventually(t, func() bool {
		lister.mu.Lock()
		defer lister.mu.Unlock()
		return lister.calls >= 3
	}, time.Second, 10*time.Millisecond, "watcher loop halted on lister error")
	assert.Equal(t, 0, rp.count())
}

// --- lifecycle --------------------------------------------------------

func TestWatcher_CloseIsIdempotent(t *testing.T) {
	w, err := stuck.New(newWatcherDeps(&fakeLister{}), stuck.WatcherConfig{
		ScanInterval: 50 * time.Millisecond,
	})
	require.NoError(t, err)
	w.Start(context.Background())
	w.Close()
	w.Close() // must not panic or block
}
