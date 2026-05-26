package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// Cold-boot recovery pack — ported from nanite's internal/service/recovery_pack.go
// (CW-20260525-0001) and adapted to torque's boot/dispatch shape.
//
// The problem is the same in both apps: when a long-lived agent session is
// re-spawned after its prior process is gone (daemon restart, orphan-sweep
// crash-mark, or planstart.Redispatch re-booting an exited orchestrator on a
// plan it was mid-walk), the fresh agent process cold-boots with only its
// kickoff prompt and loses the prior session's conversational context.
//
// The fix is the same shape: detect cold-boot-with-prior-history, build a
// bounded "<recovered-session-context>" block (last ~20 turns, each capped at
// ~1500 chars, with an explicit "this is RECOVERED CONTEXT — do not re-answer"
// instruction), thread it into the new boot so the agent resumes from recovered
// context instead of starting blind, write the full pack to
// <bootDir>/recovery.md as a re-readable pointer, and emit a
// recovery.pack_planted event.
//
// The two material divergences from nanite (both forced by torque's
// architecture — see the port report):
//
//  1. Transcript source. Nanite replays the `messages` table (clean
//     user/assistant rows). Torque has no per-session conversational message
//     table — its messages table is the inter-agent envelope substrate. The
//     authoritative per-session turn trace in torque is the stream sidecar at
//     <WorkspaceDir>/logs/stream.jsonl (typed llmtypes.StreamEvent JSONL,
//     assistant-side only: deltas + tool_use; the user/kickoff prompt is
//     delivered via SendInput and never lands in the event stream). The
//     recovered block is therefore the prior session's own output trace —
//     which is exactly what a resuming orchestrator needs ("here is what I
//     did before I went away").
//
//  2. Injection point. Nanite re-enters driveBootSession per user turn and
//     prepends the pack to that turn's payload. Torque Boots once and fires a
//     kickoff via AutoFireFirstTurn; there is no per-turn re-entry. The pack
//     is threaded through Options.SystemPrompt (which composeSystemPrompt +
//     kickoffMarkdown surface in the planted boot.md the agent reads on its
//     first turn) and the full pack is written to <bootDir>/recovery.md after
//     Boot returns (bootDir is known only post-Boot).

const (
	// recoveryHistoryTurns is the number of trailing reconstructed turns
	// (assistant text + tool calls, coalesced from the stream) replayed
	// inline in the recovery pack. Matches nanite's recoveryHistoryMessages.
	recoveryHistoryTurns = 20

	// recoveryTurnMaxChars bounds each replayed turn so a single long
	// assistant turn can't blow out the pack; the full transcript stays
	// available via the on-disk stream.jsonl + the recovery.md pointer.
	// Matches nanite's recoveryMessageMaxChars.
	recoveryTurnMaxChars = 1500

	// recoveryPackFileName is the boot-dir file the full pack is also
	// written to, so the agent can reread recovered context on demand.
	// Matches nanite's recoveryPackFileName.
	recoveryPackFileName = "recovery.md"
)

// RecoveryTurn is one reconstructed turn from a prior session's stream trace.
// Role is "assistant" for coalesced text deltas and "tool" for a tool_use
// event. The recovery pack does not reconstruct user turns because torque's
// stream.jsonl carries only the assistant-side event stream.
type RecoveryTurn struct {
	Role string
	Text string
}

// ShouldBuildRecoveryPack reports whether a cold-booted session warrants a
// recovery pack: it cold-booted (the prior session's process is gone — the
// host restarted, the orphan sweep marked it crashed, or planstart.Redispatch
// re-booted an exited orchestrator) AND it has prior reconstructed turns. A
// brand-new session has no prior trace and is intentionally skipped.
//
// Mirrors nanite's shouldBuildRecoveryPack(coldBooted, priorMessageCount).
func ShouldBuildRecoveryPack(coldBooted bool, priorTurnCount int) bool {
	return coldBooted && priorTurnCount > 0
}

// streamEventForRecovery is the subset of streamEventLine the recovery pack
// reads back. Kept separate from streamEventLine (the write shape) so a future
// stream-line schema change on the write side doesn't silently break the read.
type streamEventForRecovery struct {
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
	ToolUse *struct {
		Name string `json:"name,omitempty"`
	} `json:"tool_use,omitempty"`
}

// ReconstructTurnsFromStream reads a prior session's stream.jsonl and coalesces
// it into a chronological (oldest→newest) slice of reconstructed turns,
// returning at most the trailing `maxTurns`.
//
// Coalescing rules:
//   - Consecutive `delta` events are concatenated into a single assistant turn
//     (the stream emits one delta per text fragment). A `tool_use` event
//     flushes the in-progress assistant turn and emits a `tool` turn, so turn
//     ordering reflects the real interleave of text and tool calls.
//   - `usage`, `done`, `error`, `session_id`, `thinking` events are skipped —
//     they carry no conversational content for the resuming agent.
//
// Best-effort: a missing/unreadable file returns (nil, nil); malformed JSONL
// lines are skipped individually. The producer (stream sidecar) never blocks
// on this reader and vice-versa.
func ReconstructTurnsFromStream(streamPath string, maxTurns int) ([]RecoveryTurn, error) {
	if strings.TrimSpace(streamPath) == "" {
		return nil, nil
	}
	f, err := os.Open(streamPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var turns []RecoveryTurn
	var pendingAssistant strings.Builder
	// appendTurn appends a completed turn and rolling-trims to the trailing
	// maxTurns. Trimming as we go (rather than once at the end) bounds memory to
	// the window we'll return without capping how far we scan — so the window
	// always reflects the MOST RECENT turns even for a long-lived session whose
	// stream has many thousands of events. Completed turns are immutable, so
	// trimming them never loses context still being accumulated (that lives in
	// pendingAssistant, which is independent of this slice).
	appendTurn := func(t RecoveryTurn) {
		turns = append(turns, t)
		if maxTurns > 0 && len(turns) > maxTurns {
			turns = turns[len(turns)-maxTurns:]
		}
	}
	flushAssistant := func() {
		text := strings.TrimSpace(pendingAssistant.String())
		pendingAssistant.Reset()
		if text != "" {
			appendTurn(RecoveryTurn{Role: "assistant", Text: text})
		}
	}

	// bufio.Reader (not Scanner): a single tool_use line can carry a large JSON
	// payload (multi-MiB file diffs, etc.). bufio.Scanner has a fixed max token
	// size and STOPS PERMANENTLY when a line exceeds it — for a long-lived
	// session that's exactly the recovery target, the trailing turns (the ones
	// we most want) would be silently dropped. Reader.ReadString grows its
	// internal buffer as needed and reports EOF cleanly. A single oversized line
	// still costs that line's worth of memory, but it doesn't poison the rest of
	// the scan, and unparseable lines are skipped individually as before.
	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if s := strings.TrimSpace(line); s != "" {
			var ev streamEventForRecovery
			if jerr := json.Unmarshal([]byte(s), &ev); jerr == nil {
				switch ev.Type {
				case "delta":
					pendingAssistant.WriteString(ev.Content)
				case "tool_use":
					flushAssistant()
					name := ""
					if ev.ToolUse != nil {
						name = strings.TrimSpace(ev.ToolUse.Name)
					}
					if name == "" {
						name = "(tool)"
					}
					appendTurn(RecoveryTurn{Role: "tool", Text: "called " + name})
				default:
					// usage / done / error / session_id / thinking — no content.
				}
			}
			// malformed JSON — skip the individual line (matches the prior
			// scanner behavior; tests assert this).
		}
		if err != nil {
			flushAssistant()
			if err == io.EOF {
				return trailingTurns(turns, maxTurns), nil
			}
			// Partial reconstruction is still useful; return what we have plus
			// the error so the caller can log it.
			return trailingTurns(turns, maxTurns), err
		}
	}
}

// trailingTurns returns the last maxTurns entries (chronological order
// preserved). Mirrors nanite's history[len-N:] trim.
func trailingTurns(turns []RecoveryTurn, maxTurns int) []RecoveryTurn {
	if maxTurns > 0 && len(turns) > maxTurns {
		return turns[len(turns)-maxTurns:]
	}
	return turns
}

// RecoveryPackInput is the bounded, explicit input to BuildRecoveryPack.
type RecoveryPackInput struct {
	// SessionTitle is a human-readable identifier for the session being
	// resumed (e.g. "orchestrator / plan CW-PLAN-001"). Optional.
	SessionTitle string
	// Provider / Role surface session identity in the pack header. Optional.
	Provider string
	Role     string
	// Reason is the recovery trigger ("orchestrator redispatch after prior
	// session exited", etc). Optional.
	Reason string
	// PriorSessionID is the id of the session whose trace was replayed.
	// Optional; surfaced so the resuming agent can pull the full trace via
	// session tooling if it needs more than the bounded block.
	PriorSessionID string
	// History is the chronological (oldest→newest) reconstructed turns,
	// already trailing-trimmed to recoveryHistoryTurns.
	History []RecoveryTurn
	// PackPath is the boot-dir path the full pack is written to (the pointer).
	// Optional.
	PackPath string
}

// BuildRecoveryPack renders the recovered context threaded ahead of the first
// post-restart turn. It opens with an explicit "you are resuming" instruction
// so the agent treats it as recovered context (not a fresh request), summarizes
// session identity, replays the recent turns (bounded), and points at the
// on-disk pack + session tooling to fill gaps.
//
// Mirrors nanite's buildRecoveryPack, including the bounds (~20 turns,
// ~1500 chars each), the truncation marker, the oldest→newest ordering, and the
// "RECOVERED CONTEXT — do not re-answer" framing.
func BuildRecoveryPack(in RecoveryPackInput) string {
	var b strings.Builder
	b.WriteString("<recovered-session-context>\n")
	b.WriteString("You are resuming an existing torque agent session after the prior session process ended. ")
	b.WriteString("Treat everything in this block as RECOVERED CONTEXT, not a new request — do not re-answer it. ")
	b.WriteString("Use it to continue the work the prior session was doing. ")
	if strings.TrimSpace(in.PackPath) != "" {
		b.WriteString(fmt.Sprintf("The full pack is also at `%s` — reread it if you need more. ", in.PackPath))
	}
	b.WriteString("You can use the torque session/run tools and your task bundle to fill any gaps.\n\n")

	b.WriteString("## Session\n")
	if title := strings.TrimSpace(in.SessionTitle); title != "" {
		b.WriteString(fmt.Sprintf("- Title: %s\n", title))
	}
	if in.Provider != "" || in.Role != "" {
		b.WriteString(fmt.Sprintf("- Provider/role: %s / %s\n", in.Provider, in.Role))
	}
	if in.PriorSessionID != "" {
		b.WriteString(fmt.Sprintf("- Prior session: %s\n", in.PriorSessionID))
	}
	if in.Reason != "" {
		b.WriteString(fmt.Sprintf("- Recovery reason: %s\n", in.Reason))
	}

	if len(in.History) > 0 {
		b.WriteString("\n## Recent turns (oldest → newest)\n")
		for _, t := range in.History {
			text := strings.TrimSpace(t.Text)
			if text == "" {
				continue
			}
			if len(text) > recoveryTurnMaxChars {
				text = text[:recoveryTurnMaxChars] + " …[truncated]"
			}
			role := t.Role
			if role == "" {
				role = "assistant"
			}
			b.WriteString(fmt.Sprintf("**%s:** %s\n\n", role, text))
		}
	}
	b.WriteString("</recovered-session-context>")
	return b.String()
}

// WriteRecoveryPackFile writes the full pack to <bootDir>/recovery.md as a
// re-readable pointer and returns the path written. Best-effort: an empty
// bootDir is a no-op (returns "", nil); a write error returns the path and the
// error so the caller can log without failing the boot.
//
// Mirrors nanite's os.WriteFile(packPath, ...) step.
func WriteRecoveryPackFile(bootDir, pack string) (string, error) {
	if strings.TrimSpace(bootDir) == "" {
		return "", nil
	}
	packPath := filepath.Join(bootDir, recoveryPackFileName)
	if err := os.WriteFile(packPath, []byte(pack+"\n"), 0o644); err != nil {
		return packPath, err
	}
	return packPath, nil
}

// priorSessionStreamPath resolves the stream.jsonl path for a prior session
// from its persisted SessionMeta (torque.workspace_dir). Returns "" when the
// workspace dir is not recorded (predates the stamping convention, or the
// session was created by a test wiring without a workspace).
func priorSessionStreamPath(rec *sqlstore.SessionRecord) string {
	if rec == nil {
		return ""
	}
	meta := decodeMeta(rec.MetaJSON)
	workspaceDir := strings.TrimSpace(meta[metaKeyWorkspaceDir])
	if workspaceDir == "" {
		return ""
	}
	return filepath.Join(workspaceDir, "logs", "stream.jsonl")
}

// BuildRecoveryPackForPriorSession is the high-level entry point the dispatch
// path (planstart.Redispatch) calls. Given the prior (terminal) session's row,
// it reconstructs the trailing turns from that session's stream.jsonl, and —
// when there is prior history — returns the rendered recovery pack to thread
// into the new boot's Options.SystemPrompt. Returns "" when no recovery is
// warranted (no prior workspace recorded, empty trace, brand-new session) so
// the caller can fall through to a normal cold boot.
//
// Best-effort: read failures are reported via the returned error but a
// non-empty pack may still be returned alongside (partial reconstruction is
// useful). Callers should log the error and use the pack regardless.
//
// PackPath is set to the bare filename (recoveryPackFileName) — a RELATIVE
// pointer. The new boot's absolute bootDir is not known until after Boot
// returns, but the planted agent's cwd IS that bootDir, so "recovery.md"
// resolves correctly from the agent's perspective. The caller writes
// <bootDir>/recovery.md post-Boot via WriteRecoveryPackFile; the inlined block's
// "full pack at `recovery.md`" pointer then matches.
func BuildRecoveryPackForPriorSession(prior *sqlstore.SessionRecord, reason string) (pack string, turnCount int, err error) {
	if prior == nil {
		return "", 0, nil
	}
	streamPath := priorSessionStreamPath(prior)
	turns, readErr := ReconstructTurnsFromStream(streamPath, recoveryHistoryTurns)
	if !ShouldBuildRecoveryPack(true, len(turns)) {
		return "", 0, readErr
	}
	role := ""
	if meta := decodeMeta(prior.MetaJSON); meta != nil {
		role = meta["role"]
	}
	pack = BuildRecoveryPack(RecoveryPackInput{
		Provider:       prior.Provider,
		Role:           role,
		Reason:         reason,
		PackPath:       recoveryPackFileName,
		PriorSessionID: prior.ID,
		History:        turns,
	})
	return pack, len(turns), readErr
}
