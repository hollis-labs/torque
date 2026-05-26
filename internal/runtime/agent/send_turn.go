package agent

import (
	"context"
	"encoding/json"
	"fmt"

	agentlaunch "github.com/hollis-labs/agentkit/agentlaunch"
	"github.com/hollis-labs/agentkit/agentruntime/turn"
	feotel "github.com/hollis-labs/go-otel"
	"go.opentelemetry.io/otel/attribute"
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
//     via go-agent-runtime/turn.Frame — claude-code runs
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
func (m *Manager) SendTurn(ctx context.Context, sess *Session, text string) (err error) {
	if sess == nil {
		return fmt.Errorf("agent.Manager.SendTurn: nil session")
	}
	if sess.ID == "" {
		return fmt.Errorf("agent.Manager.SendTurn: session has empty ID")
	}

	// Trace the per-turn drive — the unit of work for "I asked this session
	// to do another turn." Distinct from torque.agent.boot (one-shot session
	// setup); each long-lived session has one boot span + N turn spans. text
	// is NEVER added as an attribute (per the OTel coverage guide's redaction
	// rules: prompts/completions stay off spans); only its length goes on.
	ctx, span := feotel.StartSpan(ctx, "torque.agent.turn")
	span.SetAttributes(
		attribute.String("hollis.app", "torque"),
		attribute.String("hollis.agent.id", sess.ID),
		attribute.String("hollis.task.id", sess.TaskID),
		attribute.String("hollis.provider", sess.Provider),
		attribute.String("hollis.runtime.kind", sess.RuntimeKind),
		attribute.Int("torque.turn.text_length", len(text)),
	)
	if sess.ProjectID != "" {
		span.SetAttributes(attribute.String("hollis.project.id", sess.ProjectID))
	}
	defer func() {
		if err != nil {
			span.RecordError(err)
		}
		span.End()
	}()

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
