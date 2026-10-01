package agent

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
)

// sourceBootOptions are the Options that re-launch rec's session: a
// long-lived boot of its agent profile in its workdir, with the role its
// original boot announced, bound to its task and project. Manager.
// ResumeSession and Manager.Resume both start from them, so a re-launched
// session, resumed or fresh, is the same task's session and not an unlinked
// one.
//
// A task-linked session is handed its task the way a dispatch hands it
// (CW-20261001-0249): the task row goes through the scheduler's own mapping
// (scheduler.BuildJob, then optsFromJob), so the planted task bundle
// (task.md, task.json) and the kickoff match a normal dispatch, and the task's
// kind, status, relationships, system prompt, agent file and environment are
// carried. Only the launch the session already had stays its own: its agent
// profile, workdir, role and, when the row has one, its project (the task's
// project otherwise). The session is not given the task's running run: a
// boot that carries a run id was dispatched by the scheduler, and a
// re-launched session is not the run's worker, so RunID stays 0.
//
// A task that cannot be read leaves the bare task id, and an agent file that
// cannot be loaded is dropped, each with a log line: a re-launch exists so
// the task is not stranded, so it must not fail on context it can do without.
func (m *Manager) sourceBootOptions(rec *sqlstore.SessionRecord) Options {
	base := Options{
		Mode:         ModeLongLived,
		AgentProfile: rec.AgentProfile,
		Workdir:      rec.Workdir,
	}
	if rec.ProjectID.Valid {
		base.ProjectID = rec.ProjectID.String
	}
	if rec.TaskID.Valid {
		base.TaskID = rec.TaskID.String
	}
	// Preserve the role tag the original boot used so the re-launched
	// transcript's planted CLAUDE.md still announces the same role.
	// Convention: planstart stamps SessionMeta["role"] for orchestrator
	// boots (orchestrator.SessionMetaRole) — that key is the canonical
	// human-readable role tag. Boot's own Options.Role defaults to the
	// AgentProfile when empty, so falling through here is also safe.
	if meta := decodeMeta(rec.MetaJSON); len(meta) > 0 {
		if v := meta["role"]; v != "" {
			base.Role = v
		}
	}
	if base.TaskID == "" || m == nil || m.deps == nil || m.deps.Store == nil {
		return base
	}
	task, err := m.deps.Store.GetTask(base.TaskID)
	if err != nil || task == nil {
		if err == nil {
			err = errors.New("no such task")
		}
		log.Printf("agent: re-launching session %s: task %s not readable, booting with its id only: %v", rec.ID, base.TaskID, err)
		return base
	}
	opts := optsFromJob(scheduler.BuildJob(m.deps.Store, *task, 0), base.Workdir)
	opts.Mode = base.Mode
	opts.AgentProfile = base.AgentProfile
	opts.LaunchProfile = ""
	opts.Workdir = base.Workdir
	opts.Role = base.Role
	if base.ProjectID != "" {
		opts.ProjectID = base.ProjectID
	}
	if opts.AgentFile != "" {
		if _, err := loadAgentFile(opts); err != nil {
			log.Printf("agent: re-launching session %s: the task's agent file cannot be loaded, booting without it: %v", rec.ID, err)
			opts.AgentFile = ""
		}
	}
	return opts
}

// prependSystemPrompt puts front (a resume's diagnostic note or a caller's
// system prompt) ahead of base (the task's own), so a note lands at the top
// of the planted prompt without dropping the task's.
func prependSystemPrompt(front, base string) string {
	switch {
	case front == "":
		return base
	case base == "":
		return front
	}
	return front + "\n\n" + base
}

// ResumesSession reports whether ResumeSession tries to continue rec's
// conversation rather than booting fresh (D4: the single decision point, a
// declared capability, not a runtime probe). It does when rec has a stored
// provider session id, the runtime its profile boots now is the one that
// recorded it, and Torque genuinely resumes that runtime in that kind
// (GenuinelyResumable, from the go-providers registry, CW-20261001-0174).
// Whether the resume held is the returned Session's Resumed.
func (m *Manager) ResumesSession(rec *sqlstore.SessionRecord) bool {
	if rec == nil {
		return false
	}
	return m.resumes(rec.Provider, rec.ResumeHint, "", rec.AgentProfile)
}

// ResumeSession resumes a previously booted session by sessionID using the
// per-session state persisted on the sessions row (AgentProfile, Workdir,
// ProjectID, TaskID, the provider session-id captured in the resume_hint
// column by the OnSessionID callback during the original boot). Falls back
// to fresh-boot unless ResumesSession says the profile's runtime genuinely
// resumes — the single decision point per sprint-α D4 (no per-call probes).
// The one fallback after the decision: a resume whose provider no longer
// has the session (Boot fails with provider.ErrProviderSessionLost) boots
// fresh once, with the kickoff. The returned Session's Resumed says which
// it did.
//
// Lifecycle: returns a freshly-booted (or freshly-resumed) *Session in the
// long-lived mode. Callers can then SendInput / Attach / Stop / Wait
// through the standard Manager surface. The α.4 (HITL response) path calls
// ResumeSession + SendInput; the α.5 (stuck-task recovery) path
// checkpoints the session, calls ResumeSession with DiagnosticNote set so
// the resumed transcript prepends an operator note, then optionally
// SendInputs follow-up instructions.
//
// Resume vs fresh-boot (ResumesSession, Resume in capabilities.go):
//
//   - Resumed (claude-code streaming-stdio and subprocess, opencode
//     subprocess, the ACP runtimes whose registry declares resume): the
//     persisted resume_hint is threaded into
//     Options.ProviderSessionIDOverride; Boot sets
//     StartOptions.SessionIDPreset (wrapper SessionIDPreset), which the
//     launch renders as the CLI's resume argument or an ACP session sends
//     in session/load.
//   - Fresh (everything else, including codex app-server and agy until
//     their resume is wired: CW-20261001-0180, CW-20261001-0181): same
//     AgentProfile / Workdir / ProjectID / TaskID rehydration, no
//     SessionIDPreset. The new boot gets a fresh session-id; the prior
//     session's transcript is NOT recovered.
//
// Boot-dir handling: Boot plants a fresh per-task tempdir + CLAUDE.md +
// `.mcp.json` on every call. ResumeSession does NOT attempt to re-use the
// prior session's boot-dir — that dir was tied to the original Manager
// process's loopback URL and may have been torn down by teardownSession.
// The fresh plant binds the new loopback (rebound to 127.0.0.1) and the
// resumed agent sees the new MCP loopback URL on first turn.
//
// Errors:
//   - ErrSessionNotFound: no row for sessionID.
//   - wrapped sqlstore errors: lookup failures other than not-found.
//   - ErrBootFailed (wrapped): Boot's own failure modes (adapter resolve,
//     loopback setup, plant, etc), including a fresh boot after a lost
//     session that fails too.
//
// The returned *Session represents a NEW row (fresh sessID generated by
// the Manager's IDFn). The original session row is unchanged — its
// state/exit_code/ended_at remain whatever they were before ResumeSession
// fired. Operators / dashboards link the two via the resume_hint column
// (same provider session-id), and a future enhancement can stamp a
// SessionMeta key with the prior sessID for stronger linkage.
func (m *Manager) ResumeSession(ctx context.Context, sessionID string, opts ResumeOptions) (*Session, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("agent.Manager.ResumeSession: sessionID required")
	}
	if m == nil || m.deps == nil || m.deps.Store == nil {
		return nil, fmt.Errorf("agent.Manager.ResumeSession: nil store")
	}
	if err := m.checkStopped(); err != nil {
		return nil, err
	}

	rec, err := m.deps.Store.GetSession(sessionID)
	if err != nil {
		if errors.Is(err, sqlstore.ErrSessionNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("agent.Manager.ResumeSession: get session: %w", err)
	}

	bootOpts := m.sourceBootOptions(rec)
	// The DiagnosticNote (set by α.5 stuck-task recovery) goes ahead of the
	// task's own system prompt in Options.SystemPrompt. composeSystemPrompt
	// puts the agent-file persona and the worker template before that, so the
	// note leads the task framing, not the planted file.
	bootOpts.SystemPrompt = prependSystemPrompt(opts.DiagnosticNote, bootOpts.SystemPrompt)

	var sess *Session
	if m.ResumesSession(rec) {
		// State-based resume: thread the persisted provider session-id
		// through Options.ProviderSessionIDOverride. Boot stamps
		// StartOptions.SessionIDPreset; the launch renders it into the
		// CLI's resume argument (claude --resume, opencode --session), or
		// an ACP session sends it in session/load. A lost one boots fresh.
		resume := bootOpts
		resume.ProviderSessionIDOverride = string(rec.ResumeHint)
		sess, err = bootWithFreshFallback(ctx, m.deps, resume, bootOpts, "ResumeSession "+sessionID)
	} else {
		// Fresh-boot: no resume argument; the new session gets a new
		// provider session-id on first turn.
		sess, err = Boot(ctx, m.deps, bootOpts)
	}
	if err != nil {
		return nil, err
	}
	return sess, nil
}
