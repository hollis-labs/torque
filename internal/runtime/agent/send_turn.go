package agent

import (
	"context"
	"encoding/json"
	"fmt"
)

// torqueClientVersion is the value the JSON-RPC initialize call
// reports as clientInfo.version to the codex app-server. Build-time
// constant; surfaced in the app-server's diagnostic log so operators
// can correlate torque releases against codex sessions.
const torqueClientVersion = "0.1-dev"

// SendTurn delivers a user-message turn to a running agent session.
// The delivery path is selected by the session's RuntimeKind:
//
//   - jsonrpc-stdio: lazy JSON-RPC handshake (initialize + thread/start
//     on the first turn; cache the thread id) followed by turn/start
//     with the cached threadId + the input wrapped as
//     [{type:"text", text:"..."}]. Mirrors agent-mux v005-07's
//     internal/app/codex.go::sendTurnJSONRPC (commit 08aa9b5) — the
//     only working consumer reference shape for codex app-server today.
//
//   - subprocess / streaming-stdio / pty: classic plaintext SendInput.
//     The lib's adapter / streaming-stdio runtimes write the bytes to
//     stdin verbatim; the receiving CLI parses them as user input.
//
// Callers route every turn-delivery (boot kickoff, HITL checkpoint
// response, future per-turn user input) through this entry point so
// the JSON-RPC routing lives in exactly one place. The raw
// Manager.SendInput surface stays available for callers that
// specifically need pre-framed bytes (none today outside SendTurn
// itself).
func (m *Manager) SendTurn(ctx context.Context, sess *Session, text string) error {
	if sess == nil {
		return fmt.Errorf("agent.Manager.SendTurn: nil session")
	}
	if sess.ID == "" {
		return fmt.Errorf("agent.Manager.SendTurn: session has empty ID")
	}
	switch RuntimeKind(sess.RuntimeKind) {
	case RuntimeKindJsonRpcStdio:
		return m.sendTurnJSONRPC(ctx, sess.ID, text)
	case RuntimeKindStreamingStdio:
		// claude-code runs `claude --input-format stream-json`: every
		// line on stdin must be one JSON object. Wrap the plaintext turn
		// as a stream-json user message — a raw line is rejected by
		// claude's parser. The runtime appends the framing newline.
		encoded, err := encodeStreamJSONUserMessage(text)
		if err != nil {
			return fmt.Errorf("encode streaming-stdio turn: %w", err)
		}
		return m.inner.SendInput(sess.ID, encoded)
	default:
		// subprocess / pty / empty share the raw-stdin path. PTY treats
		// the bytes as if typed at the TUI; subprocess passes them as the
		// turn prompt.
		return m.inner.SendInput(sess.ID, []byte(text))
	}
}

// sendTurnJSONRPC implements the codex app-server turn-delivery shape.
// Lazy initialize + thread/start on the first call for a session,
// caches the thread id on the Manager, and issues turn/start with the
// cached id + the user input. Reference: agent-mux v005-07
// internal/app/codex.go (commit 08aa9b5).
//
// initialize is fire-and-cache: we only need it once per session for
// the app-server to know who's calling. thread/start opens a fresh
// thread (codex CLI manages thread persistence in-memory for 30 min
// idle); the returned thread.id is cached and reused for every
// subsequent turn on this session. turn/start delivers the actual user
// message and returns when the call is accepted — NOT when the turn
// is done. Turn-complete detection rides on the
// `turn/completed` JSON-RPC notification, which torque wires via
// StartOptions.JsonRpcNotificationHook in boot.go.
func (m *Manager) sendTurnJSONRPC(ctx context.Context, sessID, text string) error {
	threadID, cached := m.lookupCodexThread(sessID)
	if !cached {
		initParams := map[string]any{
			"clientInfo": map[string]any{
				"name":    "torque",
				"version": torqueClientVersion,
			},
		}
		if _, err := m.inner.JsonRpcCall(ctx, sessID, "initialize", initParams); err != nil {
			return fmt.Errorf("jsonrpc initialize: %w", err)
		}
		startRes, err := m.inner.JsonRpcCall(ctx, sessID, "thread/start", map[string]any{})
		if err != nil {
			return fmt.Errorf("jsonrpc thread/start: %w", err)
		}
		var parsed struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		}
		if err := json.Unmarshal(startRes, &parsed); err != nil {
			return fmt.Errorf("decode thread/start response: %w", err)
		}
		if parsed.Thread.ID == "" {
			return fmt.Errorf("thread/start returned empty thread.id")
		}
		threadID = parsed.Thread.ID
		m.cacheCodexThread(sessID, threadID)
	}
	if _, err := m.inner.JsonRpcCall(ctx, sessID, "turn/start", map[string]any{
		"threadId": threadID,
		"input": []map[string]any{
			{"type": "text", "text": text},
		},
	}); err != nil {
		return fmt.Errorf("jsonrpc turn/start: %w", err)
	}
	return nil
}

// lookupCodexThread returns the cached codex thread id for sessID, or
// the empty string + false when no thread has been started yet.
func (m *Manager) lookupCodexThread(sessID string) (string, bool) {
	v, ok := m.codexThreads.Load(sessID)
	if !ok {
		return "", false
	}
	id, _ := v.(string)
	return id, id != ""
}

// cacheCodexThread records the codex thread id for sessID. Idempotent:
// repeated calls with the same id no-op; calls with a different id
// (shouldn't happen — thread/start fires once per session) overwrite,
// matching the agent-mux reference's sync.Map.Store semantics.
func (m *Manager) cacheCodexThread(sessID, threadID string) {
	m.codexThreads.Store(sessID, threadID)
}

// forgetCodexThread drops the cached thread id for sessID. Called by
// the Manager's teardown path when a session reaches terminal state so
// the map doesn't grow without bound across long daemon uptimes.
func (m *Manager) forgetCodexThread(sessID string) {
	m.codexThreads.Delete(sessID)
}
