package steering_test

import (
	"strings"
	"testing"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/stretchr/testify/assert"

	"github.com/hollis-labs/torque/internal/runtime/steering"
)

func userAddr() gomsg.Address {
	return gomsg.Address{Kind: gomsg.KindUser, Authority: "local", ID: "operator"}
}

func sessionAddr(id string) gomsg.Address {
	return gomsg.Address{Kind: gomsg.KindSession, Authority: "local", ID: id}
}

func TestRenderTurn_HeaderIdentifiesSteering(t *testing.T) {
	env := gomsg.Envelope{
		ID:      "ENV-1",
		Kind:    gomsg.MsgKindNotice,
		From:    userAddr(),
		To:      sessionAddr("SES-1"),
		Payload: []byte(`"focus on the failing test"`),
	}
	out := steering.RenderTurn(env)
	assert.True(t, strings.HasPrefix(out, "[steering message"), "got %q", out)
	assert.Contains(t, out, "kind=notice")
	assert.Contains(t, out, userAddr().URN())
	assert.Contains(t, out, "envelope ENV-1")
	assert.Contains(t, out, "focus on the failing test")
}

func TestRenderBody_JSONStringLiteral(t *testing.T) {
	env := gomsg.Envelope{To: sessionAddr("S"), Payload: []byte(`"plain steering text"`)}
	assert.Contains(t, steering.RenderTurn(env), "plain steering text")
}

func TestRenderBody_KnownTextKey(t *testing.T) {
	for _, key := range []string{"text", "message", "body", "note", "content", "prompt"} {
		env := gomsg.Envelope{To: sessionAddr("S"), Payload: []byte(`{"` + key + `":"steer via ` + key + `"}`)}
		out := steering.RenderTurn(env)
		assert.Contains(t, out, "steer via "+key, "key %q", key)
		assert.NotContains(t, out, "{", "key %q should not pretty-print the object", key)
	}
}

func TestRenderBody_TextContentTypeSkipsJSON(t *testing.T) {
	// A text/* content type is authoritative: even JSON-looking bytes are
	// delivered verbatim, not decoded.
	env := gomsg.Envelope{
		To:          sessionAddr("S"),
		ContentType: "text/plain",
		Payload:     []byte(`{"text":"do not decode me"}`),
	}
	out := steering.RenderTurn(env)
	assert.Contains(t, out, `{"text":"do not decode me"}`)
}

func TestRenderBody_UnknownObjectPrettyPrints(t *testing.T) {
	env := gomsg.Envelope{To: sessionAddr("S"), Payload: []byte(`{"severity":"warn","count":3}`)}
	out := steering.RenderTurn(env)
	assert.Contains(t, out, "severity")
	assert.Contains(t, out, "warn")
}

func TestRenderBody_EmptyPayload(t *testing.T) {
	env := gomsg.Envelope{To: sessionAddr("S")}
	assert.Contains(t, steering.RenderTurn(env), "(no payload)")
}

func TestRenderBody_NonJSONFallsBackToRaw(t *testing.T) {
	env := gomsg.Envelope{To: sessionAddr("S"), Payload: []byte("not json at all")}
	assert.Contains(t, steering.RenderTurn(env), "not json at all")
}
