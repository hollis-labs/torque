package agent

import (
	"context"
	"encoding/json"
	"fmt"

	agentlaunch "github.com/hollis-labs/go-agent-launch/agentlaunch"
	"github.com/hollis-labs/go-agent-runtime/turn"
)

// torqueClientVersion is the value the JSON-RPC initialize call
// reports as clientInfo.version to the codex app-server. Build-time
// constant; surfaced in the app-server's diagnostic log so operators
// can correlate torque releases against codex sessions.
const torqueClientVersion = "0.1-dev"

// SendTurn delivers a user-message turn to a running agent session.
// The delivery path is selected by the session's RuntimeKind:
//
//   - jsonrpc-stdio: go-agent-runtime's Codex app-server helper runs
//     the lazy initialize + thread/start handshake, caches thread.id,
//     and sends turn/start with a single text input block.
//
//   - streaming-stdio: the turn is wrapped as a stream-json user
//     message ({"type":"user","message":{"role":"user","content":...}})
//     via encodeStreamJSONUserMessage — claude-code runs
//     `--input-format stream-json` and rejects a raw plaintext line.
//
//   - subprocess / pty: classic plaintext SendInput. The lib writes the
//     bytes to stdin verbatim; the receiving CLI parses them as user
//     input.
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
		return m.codexTurns.SendTurn(ctx, sess.ID, codexRPCSender{mgr: m, sessID: sess.ID}, text, turn.CodexAppServerOptions{
			ClientName:    "torque",
			ClientVersion: torqueClientVersion,
			CWD:           sess.Workdir,
		})
	case RuntimeKindStreamingStdio:
		// claude-code runs `claude --input-format stream-json`: every
		// line on stdin must be one JSON object. Wrap the plaintext turn
		// as a stream-json user message — a raw line is rejected by
		// claude's parser. The runtime appends the framing newline.
		encoded, err := turn.Frame(text, turn.Options{
			Provider: "claude",
			Runtime:  agentlaunch.RuntimeKind(sess.RuntimeKind),
		})
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

type codexRPCSender struct {
	mgr    *Manager
	sessID string
}

func (s codexRPCSender) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return s.mgr.inner.JsonRpcCall(ctx, s.sessID, method, params)
}
