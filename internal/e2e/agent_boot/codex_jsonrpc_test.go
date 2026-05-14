package agent_boot

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/runtime/agent"
)

// TestBoot_ModeOneShot_CodexJsonRpcStdio_Handshake exercises the full
// codex app-server turn-delivery flow end-to-end through agent.Boot:
//
//  1. profile.Provider="codex" with empty profile.RuntimeKind → matrix
//     resolves to RuntimeKindJsonRpcStdio.
//  2. Boot wires StartOptions.AutoFireFirstTurn=false (the lib's
//     plaintext SendInput path doesn't work on JSON-RPC sessions) and
//     calls Manager.SendTurn in the ModeOneShot block.
//  3. SendTurn fires the cold-cache JSON-RPC handshake: initialize +
//     thread/start (decoding {thread:{id}} from the scripted response)
//     + turn/start. The fakeSession.Call surface records each
//     invocation in order; we assert on the method sequence + on
//     turn/start's threadId param (which must equal the cached id from
//     thread/start's response).
//  4. fakeSession.Call fires the `turn.completed` notification when
//     turn/start lands, which the JsonRpcNotificationHook wired by
//     boot.go translates into the oneshotDone close — boot.go's
//     ModeOneShot select unblocks and Stop+Wait finishes the lifecycle.
//     Status=Done is the success signal.
func TestBoot_ModeOneShot_CodexJsonRpcStdio_Handshake(t *testing.T) {
	const cannedThreadID = "thread-codex-test-001"
	cd := composeDeps(t, fakeRuntimeConfig{
		PTY: false,
		JsonRpcResponses: map[string]json.RawMessage{
			"thread/start": json.RawMessage(
				`{"thread": {"id": "` + cannedThreadID + `"}}`),
		},
	}, "codex")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:        "CW-TEST-CODEX-001",
		AgentProfile:  "torque-backend",
		Workdir:       t.TempDir(),
		Mode:          agent.ModeOneShot,
		OneShotPrompt: "audit the thing",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Runtime kind landed on the session row (and downstream consumers
	// can route by it).
	assert.Equal(t, "jsonrpc-stdio", sess.RuntimeKind,
		"codex profile default should resolve to jsonrpc-stdio")

	// AutoFireFirstTurn must be FALSE for JsonRpcStdio. The lib's
	// auto-fire path would call SendInput with plaintext, which the
	// codex app-server can't parse as JSON-RPC.
	assert.False(t, cd.Runtime.autoFireFirstTurn.Load(),
		"ModeOneShot must leave AutoFireFirstTurn=false (always); JsonRpcStdio additionally requires it for non-OneShot modes")

	fakeSess := cd.Runtime.lastSession()
	require.NotNil(t, fakeSess)

	// SendInput must NOT have fired — the JSON-RPC path goes through
	// Call(), not SendInput. The raw-bytes escape hatch on
	// jsonRpcStdioSession.SendInput exists but routing turns through
	// it would be a regression of the very bug this commit fixes.
	assert.Equal(t, int32(0), fakeSess.recordedSendInputCount(),
		"codex JsonRpcStdio session must not see SendInput; all turn delivery goes via Call()")

	// JSON-RPC handshake sequence: initialize → thread/start →
	// turn/start. The fakeSession.Call records each invocation in
	// arrival order.
	calls := fakeSess.recordedJsonRpcCalls()
	require.Len(t, calls, 3, "expected 3 JSON-RPC calls (initialize, thread/start, turn/start); got %v", methodsOf(calls))
	assert.Equal(t, "initialize", calls[0].Method)
	assert.Equal(t, "thread/start", calls[1].Method)
	assert.Equal(t, "turn/start", calls[2].Method)

	// initialize params carry clientInfo identifying torque.
	initParams, ok := calls[0].Params.(map[string]any)
	require.True(t, ok, "initialize params should be map[string]any, got %T", calls[0].Params)
	clientInfo, ok := initParams["clientInfo"].(map[string]any)
	require.True(t, ok, "clientInfo missing from initialize params: %v", initParams)
	assert.Equal(t, "torque", clientInfo["name"])
	assert.NotEmpty(t, clientInfo["version"], "initialize.clientInfo.version must be populated")

	// turn/start carries the cached thread id from thread/start's
	// response + wraps the user input as [{type:"text", text:"..."}].
	turnParams, ok := calls[2].Params.(map[string]any)
	require.True(t, ok, "turn/start params should be map[string]any, got %T", calls[2].Params)
	assert.Equal(t, cannedThreadID, turnParams["threadId"],
		"turn/start.threadId must equal the id from thread/start's response")
	input, ok := turnParams["input"].([]map[string]any)
	require.True(t, ok, "turn/start.input should be []map[string]any, got %T", turnParams["input"])
	require.Len(t, input, 1, "turn/start.input must wrap a single text block")
	assert.Equal(t, "text", input[0]["type"])
	assert.Equal(t, "audit the thing", input[0]["text"],
		"turn/start.input[0].text must equal the user prompt verbatim")

	// Status reflects the successful turn-complete flow:
	//   1. SendTurn fired the handshake + turn/start
	//   2. fakeSession.Call fired turn.completed notification
	//   3. boot.go's JsonRpcNotificationHook closed oneshotDone
	//   4. ModeOneShot select returned, Stop+Wait finished
	//   5. exitCode=0 → Status=Done
	assert.Equal(t, agent.StatusDone, sess.Status,
		"successful JsonRpcStdio kickoff must surface Status=Done")
}

// TestBoot_ModeLongLived_CodexJsonRpcStdio_PostStartKickoff covers the
// long-lived JsonRpcStdio path: AutoFireFirstTurn stays false (so the
// lib's auto-SendInput doesn't fire plaintext at the codex app-server),
// Boot drives the kickoff post-Start via SendTurn, and the session
// stays running afterwards (no Stop, no Wait). Status=Launching is
// expected — long-lived sessions transition to running asynchronously
// once the lib's watch goroutine observes the first state update.
func TestBoot_ModeLongLived_CodexJsonRpcStdio_PostStartKickoff(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{
		PTY: false,
		JsonRpcResponses: map[string]json.RawMessage{
			"thread/start": json.RawMessage(
				`{"thread": {"id": "thread-codex-longlived"}}`),
		},
	}, "codex")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:       "CW-TEST-CODEX-LL-001",
		AgentProfile: "torque-backend",
		Workdir:      t.TempDir(),
		Mode:         agent.ModeLongLived,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	assert.Equal(t, "jsonrpc-stdio", sess.RuntimeKind)
	assert.False(t, cd.Runtime.autoFireFirstTurn.Load(),
		"long-lived JsonRpcStdio must NOT set AutoFireFirstTurn — Boot drives kickoff post-Start")

	fakeSess := cd.Runtime.lastSession()
	require.NotNil(t, fakeSess)

	calls := fakeSess.recordedJsonRpcCalls()
	require.Len(t, calls, 3, "long-lived JsonRpcStdio kickoff must fire initialize + thread/start + turn/start; got %v", methodsOf(calls))
	assert.Equal(t, "initialize", calls[0].Method)
	assert.Equal(t, "thread/start", calls[1].Method)
	assert.Equal(t, "turn/start", calls[2].Method)
}

// TestSendTurn_RoutesNonJsonRpcThroughSendInput covers the negative
// case: subprocess / streaming-stdio sessions skip the JSON-RPC
// handshake entirely. SendTurn writes plaintext bytes via SendInput;
// no Call() invocations land.
func TestSendTurn_RoutesNonJsonRpcThroughSendInput(t *testing.T) {
	cd := composeDeps(t, fakeRuntimeConfig{PTY: false}, "claude")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := cd.Manager.Boot(ctx, agent.Options{
		TaskID:        "CW-TEST-CLAUDE-ROUTING",
		AgentProfile:  "torque-backend",
		Workdir:       t.TempDir(),
		Mode:          agent.ModeOneShot,
		OneShotPrompt: "hello there",
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	assert.Equal(t, "subprocess", sess.RuntimeKind,
		"claude profile default should resolve to subprocess (bare-mode)")

	fakeSess := cd.Runtime.lastSession()
	require.NotNil(t, fakeSess)

	assert.Equal(t, int32(1), fakeSess.recordedSendInputCount(),
		"claude (subprocess) SendTurn must route through SendInput, fired exactly once for the kickoff")
	calls := fakeSess.recordedJsonRpcCalls()
	assert.Empty(t, calls,
		"claude (subprocess) must NOT invoke JsonRpcCaller.Call; routing should bypass the JSON-RPC path")

	payloads := fakeSess.recordedSendInputs()
	require.Len(t, payloads, 1)
	assert.Equal(t, "hello there", string(payloads[0]))
}

// methodsOf is a small helper for the require.Len failure-message
// templates above — printing the recordedJsonRpcCall slice prints the
// full params (noisy); a method-only summary is the useful diagnostic.
func methodsOf(calls []recordedJsonRpcCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Method)
	}
	return out
}
