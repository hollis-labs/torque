// Package tetherexec is the Torque-side `tether` executor: it launches the
// agent for a task as a PLAIN Tether session and watches it to a result.
//
// Tether is plumbing here, never the dispatcher. Torque picks the task, builds
// the request, creates and launches the session, and keeps the
// session-to-task mapping itself (runs.tether_session_id / launch_id /
// workstream_id), because Tether drops task_id from non-operator callers.
//
// Completion (CW-20261030-0211): GET /sessions/{id}/wait returns on exit
// only, and agents usually go idle instead of exiting. So the run is marked
// done from the session's final `session.turn_output` event. Ending the
// session is then the job of Tether's opt-in `stop_after_final_turn` reaper;
// Torque stops the session itself only when that reaper is not configured or
// did not fire within its grace.
package tetherexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/tetherclient"
)

// Name is the executor's registered name.
const Name = "tether"

// Sessions is the slice of the Tether API the executor uses
// (*tetherclient.Client implements it).
type Sessions interface {
	CreateSession(ctx context.Context, req tetherclient.CreateRequest) (*tetherclient.Session, error)
	LaunchSession(ctx context.Context, id string) (*tetherclient.Session, error)
	GetSession(ctx context.Context, id string) (*tetherclient.SessionState, error)
	StopSession(ctx context.Context, id string) error
	SessionEvents(ctx context.Context, id string, limit int, cursor int64) ([]tetherclient.Event, error)
	WaitSession(ctx context.Context, id string) (*tetherclient.WaitResult, error)
}

// RunBinder records which Tether session a Torque run launched.
type RunBinder interface {
	BindRun(ctx context.Context, runID int64, tetherSessionID, launchID, workstreamID string) error
}

// Config tunes the executor. Zero values take the defaults noted.
type Config struct {
	DefaultLaunch  string        // launch id when the task names none (env TORQUE_TETHER_LAUNCH)
	DefaultWorker  string        // optional Tether worker
	PollInterval   time.Duration // session/event poll; default 3s
	DeferInterval  time.Duration // retry gap after a capacity refusal; default 15s
	DeferMax       time.Duration // stop deferring (task blocked, not failed); default 30m
	DefaultTimeout time.Duration // run ceiling after launch; default 2h
	CreateRetryGap time.Duration // pause between in-process CreateSession retries; default 2s
	StopGrace      time.Duration // wait past stop_after_final_turn before Torque stops the session; default 30s
	// StopAfterFinalTurn is a default opt-in reaper grace sent in the create
	// override (e.g. "30s"); empty leaves it to the Tether launch profile or
	// agent definition. A task can set metadata tether_stop_after_final_turn.
	StopAfterFinalTurn string
}

// createAttempts bounds in-process CreateSession tries on transport errors.
const createAttempts = 3

func (c *Config) defaults() {
	if c.CreateRetryGap <= 0 {
		c.CreateRetryGap = 2 * time.Second
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 3 * time.Second
	}
	if c.DeferInterval <= 0 {
		c.DeferInterval = 15 * time.Second
	}
	if c.DeferMax <= 0 {
		c.DeferMax = 30 * time.Minute
	}
	if c.DefaultTimeout <= 0 {
		c.DefaultTimeout = 2 * time.Hour
	}
	if c.StopGrace <= 0 {
		c.StopGrace = 30 * time.Second
	}
}

// Executor implements executor.Executor over Tether sessions.
type Executor struct {
	sessions Sessions
	binder   RunBinder
	cfg      Config
}

// New returns the executor. binder may be nil (the mapping is then only in
// the run's events and logs).
func New(s Sessions, binder RunBinder, cfg Config) *Executor {
	cfg.defaults()
	return &Executor{sessions: s, binder: binder, cfg: cfg}
}

func (e *Executor) Name() string { return Name }

func (e *Executor) Capabilities() executor.ExecutorCapabilities {
	return executor.ExecutorCapabilities{SupportsStreaming: true, SupportsTools: true}
}

// Validate refuses a job that names no launch before a run is spent on it.
func (e *Executor) Validate(job *executor.ExecutionJob) error {
	if err := job.Validate(); err != nil {
		return err
	}
	if e.sessions == nil {
		return executor.NewPermanentError(errors.New("tether executor: TORQUE_TETHER_URL is not set"))
	}
	if e.launchFor(job) == "" {
		return executor.NewPermanentError(errors.New(
			"tether executor: no launch id: set task metadata tether_launch (or launch_profile), or TORQUE_TETHER_LAUNCH"))
	}
	return nil
}

func (e *Executor) launchFor(job *executor.ExecutionJob) string {
	if v := metaString(job.Metadata, "tether_launch"); v != "" {
		return v
	}
	if job.LaunchProfile != "" {
		return job.LaunchProfile
	}
	return e.cfg.DefaultLaunch
}

// IdempotencyKey is stable per run: a retried create for the same run returns
// the same Tether session, while a new attempt (new run id) gets a new one.
func IdempotencyKey(taskID string, runID int64) string {
	return fmt.Sprintf("torque/%s/run-%d", taskID, runID)
}

// isLaunchConflict reports a 409 that is not an idempotency_conflict.
func isLaunchConflict(err error) bool {
	var ae *tetherclient.APIError
	return errors.As(err, &ae) && ae.Status == 409 && ae.Code != tetherclient.CodeIdempotencyConflict
}

func isClientError(err error) bool {
	var ae *tetherclient.APIError
	return errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 500
}

// Run creates and launches the session, then watches it to an outcome.
//
// Failure mapping: a refused request (4xx: unknown launch, key conflict) is a
// permanent block; a capacity refusal defers (and blocks, never fails, once
// DeferMax passes); an agent failure turn or non-zero exit before any final
// turn is `failed` (the task's on_fail policy applies); a final turn is
// `done`; transport trouble returns an error so the scheduler's retry applies.
// Cancellation (ctx) stops the Tether session and returns ctx.Err().
func (e *Executor) Run(ctx context.Context, job *executor.ExecutionJob, cb executor.EventCallback) (*executor.ExecutionResult, error) {
	start := time.Now()
	emit := func(format string, a ...any) {
		if cb != nil {
			cb(executor.LogEvent("[tether] " + fmt.Sprintf(format, a...)))
		}
	}
	result := func(status, reason string) *executor.ExecutionResult {
		return &executor.ExecutionResult{Status: status, Reason: reason, Duration: time.Since(start)}
	}
	if err := e.Validate(job); err != nil {
		return result("blocked", err.Error()), err
	}
	launch := e.launchFor(job)

	req := tetherclient.CreateRequest{
		Launch:         launch,
		IdempotencyKey: IdempotencyKey(job.TaskID, job.RunID),
		Worker:         firstNonEmpty(metaString(job.Metadata, "tether_worker"), e.cfg.DefaultWorker),
		PromptAppend:   buildPrompt(job),
	}
	if rt, ok := job.Metadata["tether_route"].(map[string]any); ok {
		if b, err := json.Marshal(rt); err == nil {
			req.Route = b
		}
	}
	reaper := firstNonEmpty(metaString(job.Metadata, "tether_stop_after_final_turn"), e.cfg.StopAfterFinalTurn)
	var reapDur time.Duration
	if reaper != "" {
		d, err := time.ParseDuration(reaper)
		if err != nil || d <= 0 {
			err = fmt.Errorf("tether_stop_after_final_turn %q is not a positive duration", reaper)
			return result("blocked", err.Error()), executor.NewPermanentError(err)
		}
		reapDur = d
		req.Override = `{"lifecycle":{"stop_after_final_turn":` + strconv.Quote(reaper) + `}}`
	}

	var created *tetherclient.Session
	var err error
	for attempt := 1; ; attempt++ {
		created, err = e.sessions.CreateSession(ctx, req)
		// The idempotency key makes a retry safe: if the first request landed
		// despite the transport error, the retry returns that session.
		if err == nil || ctx.Err() != nil || isClientError(err) || attempt >= createAttempts {
			break
		}
		emit("create session attempt %d failed: %v; retrying", attempt, err)
		if !sleep(ctx, e.cfg.CreateRetryGap) {
			return nil, ctx.Err()
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if isClientError(err) {
			// Unknown launch, bad body or key conflict: retrying cannot help.
			return result("blocked", "tether create session: "+err.Error()), executor.NewPermanentError(err)
		}
		return nil, fmt.Errorf("tether create session: %w", err)
	}
	sid := created.ID
	emit("session %s created (launch %s, replayed=%v)", sid, launch, created.Replayed)
	workstream := metaString(job.Metadata, "workstream_id")
	e.bind(ctx, job.RunID, sid, launch, workstream)

	// The session exists now; every exit path stops it unless it is over.
	live := true
	defer func() {
		if live {
			e.stop(sid, emit)
		}
	}()

	// Launch, deferring while Tether's execution policy refuses (paused or at
	// a cap). The session stays `created`, so this is a wait, not a failure.
	deferDeadline := time.Now().Add(e.cfg.DeferMax)
	for {
		_, err = e.sessions.LaunchSession(ctx, sid)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !tetherclient.IsCapacityRefusal(err) {
			if isLaunchConflict(err) {
				// A 409 other than a key conflict (a state race, a transient
				// policy refusal) leaves the session `created`: defer like a
				// capacity refusal rather than fail the task.
			} else if isClientError(err) {
				return result("failed", "tether launch: "+err.Error()), nil
			} else {
				return nil, fmt.Errorf("tether launch: %w", err)
			}
		}
		if time.Now().After(deferDeadline) {
			return result("blocked", fmt.Sprintf("tether capacity: launch deferred %s without admission (%v)", e.cfg.DeferMax, err)), nil
		}
		emit("launch deferred: %v", err)
		if !sleep(ctx, e.cfg.DeferInterval) {
			return nil, ctx.Err()
		}
	}
	emit("session %s launched", sid)

	runCtx, cancel := context.WithTimeout(ctx, runTimeout(job, e.cfg.DefaultTimeout))
	defer cancel()

	w, err := e.watch(runCtx, ctx, sid, reapDur, workstream, job, emit)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err() // cancelled by Torque: the deferred stop ends the session
		}
		return nil, err
	}
	w.Duration = time.Since(start)
	if w.sessionOver {
		live = false
	}
	return w.ExecutionResult, nil
}

type watchResult struct {
	*executor.ExecutionResult
	sessionOver bool
}

// watch polls the session state and its events until the run has an outcome.
func (e *Executor) watch(runCtx, parent context.Context, sid string, reapDur time.Duration, workstream string, job *executor.ExecutionJob, emit func(string, ...any)) (*watchResult, error) {
	var lastSeq int64
	var finalAt time.Time
	var final *tetherclient.TurnOutput
	boundWS := workstream != ""
	pollErrs := 0
	stalled := false
	abnormal := false // a state change to killed/failed, or a non-zero exit, was seen

	for {
		st, serr := e.sessions.GetSession(runCtx, sid)
		switch {
		case serr == nil:
			pollErrs = 0
			if !boundWS && st.WorkstreamID != "" {
				boundWS = true
				e.bind(runCtx, job.RunID, sid, st.LaunchID, st.WorkstreamID)
			}
			if st.Stalled && !stalled {
				stalled = true
				emit("session stalled: %s", st.StallReason)
			}
		case runCtx.Err() != nil:
			// falls through to the wait below, which handles the ceiling
		case tetherclient.IsNotFound(serr):
			return &watchResult{ExecutionResult: &executor.ExecutionResult{Status: "failed", Reason: "tether session disappeared: " + serr.Error()}, sessionOver: true}, nil
		default:
			pollErrs++
			if pollErrs >= 10 {
				return nil, fmt.Errorf("tether session %s unreadable: %w", sid, serr)
			}
			emit("poll error (%d): %v", pollErrs, serr)
		}

		// New events, oldest first. The events endpoint is newest-first and
		// paged, so walk back until we reach what we have already seen: a busy
		// streaming session can emit more than one page between polls.
		evs, eerr := e.newEvents(runCtx, sid, lastSeq)
		if eerr == nil {
			for _, ev := range evs {
				lastSeq = ev.Seq
				if ev.Kind == tetherclient.EventStateChanged {
					var sc struct {
						To       string `json:"to"`
						ExitCode *int   `json:"exit_code"`
					}
					if json.Unmarshal([]byte(ev.PayloadJSON), &sc) == nil {
						if sc.To == tetherclient.StateKilled || sc.To == tetherclient.StateFailed ||
							(sc.ExitCode != nil && *sc.ExitCode != 0) {
							abnormal = true
						}
					}
					continue
				}
				to, ok := ev.TurnOutput()
				if !ok {
					continue
				}
				switch to.Kind {
				case tetherclient.OutputKindFinal:
					if final == nil {
						final = to
						finalAt = time.Now()
						emit("final turn output (turn %s)", to.TurnID)
					}
				case tetherclient.OutputKindFailure:
					return &watchResult{ExecutionResult: &executor.ExecutionResult{
						Status: "failed", Reason: "agent turn failed: " + excerpt(to.Text, 500),
					}}, nil
				case tetherclient.OutputKindQuestion, tetherclient.OutputKindApproval:
					emit("agent is waiting on a %s: %s", to.Kind, excerpt(to.Text, 300))
				}
			}
		}

		if serr == nil && tetherclient.IsTerminalState(st.State) {
			e.fillRoutedText(runCtx, sid, final)
			return terminal(st, final, abnormal), nil
		}

		// A final turn with the session still alive means it went idle. The
		// opt-in reaper should end it; Torque stops it only past the
		// reaper's grace, or at once when no reaper is configured.
		if final != nil {
			wait := time.Duration(0)
			if reapDur > 0 {
				wait = reapDur + e.cfgStopGrace()
			}
			if time.Since(finalAt) >= wait {
				note := "stop_after_final_turn not configured; Torque stopped the idle session"
				if reapDur > 0 {
					note = fmt.Sprintf("stop_after_final_turn=%s did not end the session within %s; Torque stopped it", reapDur, wait)
				}
				emit("%s", note)
				e.stop(sid, emit)
				return &watchResult{ExecutionResult: doneResult(final, note), sessionOver: true}, nil
			}
		}

		if !sleep(runCtx, e.cfg.PollInterval) {
			if parent.Err() != nil {
				return nil, parent.Err()
			}
			// Run ceiling reached.
			e.stop(sid, emit)
			if final != nil {
				return &watchResult{ExecutionResult: doneResult(final, "run timeout after final turn; session stopped"), sessionOver: true}, nil
			}
			return &watchResult{ExecutionResult: &executor.ExecutionResult{
				Status: "failed", Reason: "tether run timed out before a final turn; session stopped",
			}, sessionOver: true}, nil
		}
	}
}

// fillRoutedText recovers the final text for a routed session: when the launch
// routes the final kind, the turn_output event carries only a message_id and
// the text is staged elsewhere. The session has exited, so /wait answers at
// once with the newest turn result.
func (e *Executor) fillRoutedText(ctx context.Context, sid string, final *tetherclient.TurnOutput) {
	if final == nil || final.Text != "" {
		return
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if w, err := e.sessions.WaitSession(wctx, sid); err == nil && w.OutputText != "" {
		final.Text = w.OutputText
	}
}

// newEvents returns the events with seq > after, oldest first, paging back
// through the newest-first endpoint until it reaches events already seen.
func (e *Executor) newEvents(ctx context.Context, sid string, after int64) ([]tetherclient.Event, error) {
	const pageSize, maxPages = 200, 25
	var out []tetherclient.Event
	var cursor int64
	for page := 0; page < maxPages; page++ {
		evs, err := e.sessions.SessionEvents(ctx, sid, pageSize, cursor)
		if err != nil {
			return nil, err
		}
		reached := len(evs) < pageSize
		oldest := int64(0)
		for _, ev := range evs {
			if ev.Seq <= after {
				reached = true
				continue
			}
			out = append(out, ev)
			if oldest == 0 || ev.Seq < oldest {
				oldest = ev.Seq
			}
		}
		if reached || oldest == 0 {
			break
		}
		cursor = oldest
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

func (e *Executor) cfgStopGrace() time.Duration { return e.cfg.StopGrace }

func terminal(st *tetherclient.SessionState, final *tetherclient.TurnOutput, abnormal bool) *watchResult {
	code := -1
	if st.ExitCode != nil {
		code = *st.ExitCode
	}
	over := func(r *executor.ExecutionResult) *watchResult {
		return &watchResult{ExecutionResult: r, sessionOver: true}
	}
	if final != nil {
		// A final turn is success however the session ended afterwards
		// (reaper stop, exit).
		return over(doneResult(final, fmt.Sprintf("session %s, exit %d", st.State, code)))
	}
	switch st.State {
	case tetherclient.StateCompleted:
		if abnormal {
			// Tether reports a stopped session as killed and then settles it
			// to `completed` with no exit code; the state change history is
			// the only record that it did not finish by itself.
			return over(&executor.ExecutionResult{Status: "failed", Reason: "tether session was stopped or killed before a final turn", ExitCode: &code})
		}
		return over(&executor.ExecutionResult{Status: "done", Reason: "tether session exited 0 without a final turn output", ExitCode: &code})
	case tetherclient.StateKilled:
		return over(&executor.ExecutionResult{Status: "failed", Reason: "tether session was stopped before a final turn", ExitCode: &code})
	default:
		return over(&executor.ExecutionResult{Status: "failed", Reason: fmt.Sprintf("tether session failed (exit %d)", code), ExitCode: &code})
	}
}

func doneResult(final *tetherclient.TurnOutput, note string) *executor.ExecutionResult {
	r := &executor.ExecutionResult{Status: "done", Reason: "final turn: " + excerpt(final.Text, 300)}
	if final.Text != "" {
		r.TaskComment = &executor.TaskComment{
			Author:  "tether-executor",
			Content: "Final turn output (" + note + "):\n\n" + excerpt(final.Text, 3000),
		}
	}
	return r
}

func (e *Executor) bind(ctx context.Context, runID int64, sid, launch, workstream string) {
	if e.binder == nil || runID == 0 {
		return
	}
	if err := e.binder.BindRun(ctx, runID, sid, launch, workstream); err != nil {
		log.Printf("[tether-exec] bind run %d -> session %s: %v", runID, sid, err)
	}
}

// stop stops the Tether session on a context that survives cancellation.
func (e *Executor) stop(sid string, emit func(string, ...any)) {
	var err error
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = e.sessions.StopSession(ctx, sid)
		cancel()
		if err == nil {
			emit("session %s stopped", sid)
			return
		}
		time.Sleep(time.Duration(i+1) * 200 * time.Millisecond)
	}
	log.Printf("[tether-exec] stop session %s: %v", sid, err)
	emit("stop session %s failed: %v", sid, err)
}

func buildPrompt(job *executor.ExecutionJob) string {
	var b strings.Builder
	if job.SystemPrompt != "" {
		b.WriteString(job.SystemPrompt)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "You are assigned Torque task %s", job.TaskID)
	if job.TaskTitle != "" {
		fmt.Fprintf(&b, ": %s", job.TaskTitle)
	}
	b.WriteString("\n")
	if job.Description != "" {
		b.WriteString("\n")
		b.WriteString(job.Description)
		b.WriteString("\n")
	}
	b.WriteString("\nWork the task, then finish with a short final message summarising the result.\n")
	return b.String()
}

func runTimeout(job *executor.ExecutionJob, def time.Duration) time.Duration {
	if job.Limits.MaxDuration != nil && *job.Limits.MaxDuration > 0 {
		return *job.Limits.MaxDuration
	}
	var secs float64
	switch v := job.Metadata["timeout_seconds_override"].(type) {
	case float64:
		secs = v
	case int:
		secs = float64(v)
	}
	if secs >= 60 && secs <= 7200 {
		return time.Duration(secs) * time.Second
	}
	return def
}

func metaString(m map[string]any, k string) string {
	if s, ok := m[k].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func excerpt(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// sleep waits d or until ctx ends; it reports whether the full wait elapsed.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
