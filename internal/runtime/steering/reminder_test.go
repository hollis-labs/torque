package steering_test

import (
	"strings"
	"testing"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/runtime/steering"
)

func noticeEnv(id, body string) gomsg.Envelope {
	return gomsg.Envelope{
		ID:          id,
		Kind:        gomsg.MsgKindNotice,
		From:        gomsg.Address{Kind: gomsg.KindUser, Authority: "local", ID: "operator"},
		To:          sessionAddr("SES-1"),
		Payload:     []byte(body),
		ContentType: "application/json",
	}
}

func TestReminderRegistry_NilRegistrySafe(t *testing.T) {
	var r *steering.ReminderRegistry
	r.RecordDelivery("CW-T-1", noticeEnv("ENV-1", `"hi"`))
	assert.Nil(t, r.Dismiss("CW-T-1", "ENV-1"))
	pending, had := r.PendingForTurnBoundary("CW-T-1")
	assert.False(t, had)
	assert.Nil(t, pending)
	r.MarkReminded("CW-T-1", "ENV-1")
	r.Forget("CW-T-1")
	assert.Nil(t, r.Snapshot("CW-T-1"))
}

func TestReminderRegistry_EmptyTaskIDIsNoop(t *testing.T) {
	r := steering.NewReminderRegistry()
	r.RecordDelivery("", noticeEnv("ENV-X", `"hi"`))
	pending, had := r.PendingForTurnBoundary("")
	assert.False(t, had)
	assert.Empty(t, pending)
}

func TestReminderRegistry_RecordAndPending(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{t: now}
	r := steering.NewReminderRegistry().WithNow(clk.Now)

	r.RecordDelivery("CW-T-1", noticeEnv("ENV-A", `"first"`))
	clk.advance(5 * time.Second)
	r.RecordDelivery("CW-T-1", noticeEnv("ENV-B", `"second"`))

	pending, had := r.PendingForTurnBoundary("CW-T-1")
	require.True(t, had, "turn that received injections must report hadInjection")
	require.Len(t, pending, 2)
	assert.Equal(t, "ENV-A", pending[0].EnvelopeID, "oldest first")
	assert.Equal(t, "ENV-B", pending[1].EnvelopeID)
	assert.Equal(t, "first", pending[0].Subject)
}

func TestReminderRegistry_TurnWithoutInjectionReturnsNothing(t *testing.T) {
	r := steering.NewReminderRegistry()
	r.RecordDelivery("CW-T-1", noticeEnv("ENV-A", `"hi"`))

	// First call drains the turn flag.
	pending, had := r.PendingForTurnBoundary("CW-T-1")
	require.True(t, had)
	require.NotEmpty(t, pending)

	// Second call with no new injection: had=false, no pending — spec's
	// "if a message was injected during THIS turn" gate.
	pending, had = r.PendingForTurnBoundary("CW-T-1")
	assert.False(t, had)
	assert.Empty(t, pending)
}

func TestReminderRegistry_MarkRemindedStopsReReminder(t *testing.T) {
	r := steering.NewReminderRegistry()
	r.RecordDelivery("CW-T-1", noticeEnv("ENV-A", `"hi"`))

	pending, had := r.PendingForTurnBoundary("CW-T-1")
	require.True(t, had)
	require.Len(t, pending, 1)
	r.MarkReminded("CW-T-1", "ENV-A")

	// New injection on the next turn — the previously-reminded envelope
	// must NOT re-surface (the "stop nagging" rule). The fresh envelope
	// does.
	r.RecordDelivery("CW-T-1", noticeEnv("ENV-B", `"another"`))
	pending, had = r.PendingForTurnBoundary("CW-T-1")
	require.True(t, had)
	require.Len(t, pending, 1)
	assert.Equal(t, "ENV-B", pending[0].EnvelopeID)
}

func TestReminderRegistry_DismissPreventsReminder(t *testing.T) {
	r := steering.NewReminderRegistry()
	r.RecordDelivery("CW-T-1", noticeEnv("ENV-A", `"please"`))
	r.RecordDelivery("CW-T-1", noticeEnv("ENV-B", `"thanks"`))

	got := r.Dismiss("CW-T-1", "ENV-A", "ENV-UNKNOWN")
	assert.Equal(t, []string{"ENV-A"}, got, "unknown ids are silently dropped")

	pending, had := r.PendingForTurnBoundary("CW-T-1")
	require.True(t, had)
	require.Len(t, pending, 1)
	assert.Equal(t, "ENV-B", pending[0].EnvelopeID)
}

func TestReminderRegistry_DismissedStaysDismissedAcrossReDelivery(t *testing.T) {
	r := steering.NewReminderRegistry()
	env := noticeEnv("ENV-A", `"hi"`)
	r.RecordDelivery("CW-T-1", env)
	r.Dismiss("CW-T-1", "ENV-A")

	// Operator re-sends the same envelope id (defensive — shouldn't
	// happen since Bridge.Consume excludes from Inbox, but the agent
	// having already said "ignore" must win).
	r.RecordDelivery("CW-T-1", env)
	pending, had := r.PendingForTurnBoundary("CW-T-1")
	assert.True(t, had, "the turn DID have an injection, even if all envelopes are dismissed")
	assert.Empty(t, pending, "dismissed envelope must not re-surface")
}

func TestReminderRegistry_ForgetClears(t *testing.T) {
	r := steering.NewReminderRegistry()
	r.RecordDelivery("CW-T-1", noticeEnv("ENV-A", `"x"`))
	r.Forget("CW-T-1")
	assert.Nil(t, r.Snapshot("CW-T-1"))
	pending, had := r.PendingForTurnBoundary("CW-T-1")
	assert.False(t, had)
	assert.Empty(t, pending)
}

func TestReminderRegistry_PerTaskIsolation(t *testing.T) {
	r := steering.NewReminderRegistry()
	r.RecordDelivery("CW-T-1", noticeEnv("ENV-A", `"a"`))
	r.RecordDelivery("CW-T-2", noticeEnv("ENV-B", `"b"`))

	pending, had := r.PendingForTurnBoundary("CW-T-1")
	require.True(t, had)
	require.Len(t, pending, 1)
	assert.Equal(t, "ENV-A", pending[0].EnvelopeID)

	pending, had = r.PendingForTurnBoundary("CW-T-2")
	require.True(t, had)
	require.Len(t, pending, 1)
	assert.Equal(t, "ENV-B", pending[0].EnvelopeID)
}

func TestRenderReminder_MultipleEnvelopes(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 30, 0, time.UTC)
	pending := []*steering.PendingEnvelope{
		{
			EnvelopeID: "ENV-A",
			From:       "msg://user/local/operator",
			Kind:       "notice",
			Subject:    "first message",
			InjectedAt: now.Add(-20 * time.Second),
		},
		{
			EnvelopeID: "ENV-B",
			From:       "msg://user/local/operator",
			Kind:       "notice",
			Subject:    "",
			InjectedAt: now.Add(-5 * time.Second),
		},
	}
	out := steering.RenderReminder(pending, now)
	assert.Contains(t, out, "2 unaddressed messages")
	assert.Contains(t, out, "envelope=ENV-A")
	assert.Contains(t, out, "envelope=ENV-B")
	assert.Contains(t, out, "subject: first message")
	assert.Contains(t, out, "torque_steering_dismiss")
	assert.Contains(t, out, "age=20s")
}

func TestRenderReminder_Empty(t *testing.T) {
	assert.Empty(t, steering.RenderReminder(nil, time.Now()))
	assert.Empty(t, steering.RenderReminder([]*steering.PendingEnvelope{}, time.Now()))
}

func TestPreviewSubject_LongPayloadTruncates(t *testing.T) {
	long := strings.Repeat("abcdefghij", 30) // 300 chars
	env := noticeEnv("ENV-LONG", `"`+long+`"`)
	r := steering.NewReminderRegistry()
	r.RecordDelivery("CW-T-1", env)
	snap := r.Snapshot("CW-T-1")
	require.Len(t, snap, 1)
	// Includes ellipsis when truncated; total length ≤ subjectMaxLen.
	assert.LessOrEqual(t, len(snap[0].Subject), 120)
	assert.True(t, strings.HasSuffix(snap[0].Subject, "…"))
}

type fakeClock struct {
	t time.Time
}

func (c *fakeClock) Now() time.Time { return c.t }

func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }
