package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"

	"github.com/hollis-labs/torque/internal/broker"
	"github.com/hollis-labs/torque/internal/runtime/steering"
)

// registerBrokerTools surfaces the typed envelope broker (CW-20260503-0013)
// over MCP. Tools reply with a domain error when the adapter has no broker
// wired (mcp-only stdio path), mirroring the sessionmgr contract.
func (a *Adapter) registerBrokerTools() {
	a.addTool(newTool("torque_broker_send",
		withDescription(`Send a typed envelope through the Torque broker (notice|status_update|handoff|escalation|response).
Use for fire-and-forget envelopes (notice, status_update, handoff) and one-shot escalations. For request/reply with a blocking wait, use torque_broker_request.
Validation: kind must be a known envelope type; from/to must be canonical msg:// URNs; payload size capped per MaxPayloadBytes; escalation requires severity in {info,warn,error,critical} and a non-empty reason.
Response shape: data = <Envelope> singleton — id, kind, from, to, thread_id, in_reply_to, created_at, payload, etc.
Example: {"kind":"notice","from":"msg://agent/test/alice","to":"msg://agent/test/bob","payload":"{\"hello\":\"world\"}"}`),
		withString("kind", required(), desc("Envelope kind: notice|status_update|handoff|escalation|response")),
		withString("from", required(), desc("Sender URN (msg://kind/authority/id[/subid])")),
		withString("to", required(), desc("Recipient URN")),
		withString("payload", desc("JSON-encoded payload (string)")),
		withString("content_type", desc("Override default application/json")),
		withString("thread_id", desc("Conversation thread ID")),
		withString("in_reply_to", desc("Correlation ID — for response envelopes")),
		withString("channel", desc("Opaque UX-layer channel tag")),
		withString("metadata", desc("JSON object of string→string metadata")),
	), a.handleBrokerSend)

	a.addTool(newTool("torque_broker_request",
		withDescription(`Send a kind=request envelope and BLOCK until a correlated response arrives or the timeout expires.
Use for synchronous agent-to-agent calls (orchestrator asks reviewer for disposition; planner asks orchestrator for context). Pair with torque_broker_send (kind=response) on the responder side, or use the dispatcher's Reply helper.
timeout_seconds clamps to [1, 600]; defaults to 30. On timeout the call returns a domain error and the request envelope persists — issue torque_broker_send for a follow-up Cancel-equivalent if the request is no longer meaningful.
Response shape: data = <Envelope> for the response (kind=response, in_reply_to=<request id>).
Example: {"from":"msg://agent/test/alice","to":"msg://agent/test/bob","payload":"{\"q\":\"ping\"}","timeout_seconds":"15"}`),
		withString("from", required(), desc("Requester URN")),
		withString("to", required(), desc("Responder URN")),
		withString("payload", desc("JSON-encoded request body")),
		withString("content_type"),
		withString("thread_id"),
		withString("channel"),
		withString("metadata"),
		withString("timeout_seconds", desc("Block timeout in seconds; default 30, range [1,600]")),
	), a.handleBrokerRequest)

	a.addTool(newTool("torque_broker_inbox",
		withDescription(`Drain undelivered envelopes addressed to a recipient URN. Atomically marks returned rows DeliveredAt=now and emits envelope.delivered SSE events.
Use to poll for incoming envelopes when not running a Subscribe stream (HTTP /api/v1/messages/subscribe is the SSE alternative).
kind / channel / thread_id filter values combine via AND; within a slice values OR. Limit caps the page (0 = unlimited).
Response shape: data = {envelopes: [<Envelope>...]}.
Example: {"to":"msg://agent/test/alice","limit":"20"}`),
		withString("to", required(), desc("Recipient URN")),
		withString("kind", desc("Filter by envelope kind (comma-separated for multi)")),
		withString("channel", desc("Filter by channel (comma-separated for multi)")),
		withString("thread_id", desc("Filter by thread")),
		withString("limit", desc("Max envelopes (integer; 0 = unlimited)")),
	), a.handleBrokerInbox)

	a.addTool(newTool("torque_inbox_poll",
		withDescription(`Opt into mid-session inbox polling AND drain your inbox in one call (CW-20260518-0042).
By default Torque delivers envelopes addressed to a live agent by injecting them as the agent's next turn (inject-at-turn-boundary). An agent that is actively communicating can instead PULL its own inbox between tool calls: each torque_inbox_poll call records a polling opt-in for the 'to' URN, and while that opt-in is fresh the steering bridge stops injecting turns for that recipient — so a steering message is handled exactly once, by your poll.
The opt-in is time-bounded: it lapses after poll_ttl_seconds (returned in the response) unless you poll again. Keep polling on a cadence shorter than the TTL to stay opted in; stop polling (or pass release=true) to revert to inject-at-turn. 'to' MUST be your own address and match how senders address you (e.g. msg://agent/<authority>/<your-task-id>).
Response shape: data = {to, polling, poll_ttl_seconds, count, envelopes:[<Envelope>...], drain_hint?}.
Example: {"to":"msg://agent/local/CW-20260518-0042","limit":"20"}`),
		withString("to", required(), desc("Your own recipient URN (msg://kind/authority/id[/subid])")),
		withString("kind", desc("Filter drained envelopes by kind (comma-separated for multi)")),
		withString("channel", desc("Filter drained envelopes by channel (comma-separated for multi)")),
		withString("thread_id", desc("Filter drained envelopes by thread")),
		withString("limit", desc("Max envelopes to drain (integer; 0 = unlimited)")),
		withBoolean("release", desc("If true, drop the polling opt-in (revert to inject-at-turn) instead of refreshing it; a final drain is still returned")),
	), a.handleInboxPoll)
}

// requirePollRegistry returns the opt-in inbox-poll registry or a domain
// error when none is wired — mirroring requireBroker's contract for MCP
// hosts that do not run the steering bridge in-process.
func (a *Adapter) requirePollRegistry() (*steering.PollRegistry, error) {
	if a.pollRegistry == nil {
		return nil, errors.New("inbox polling not available on this MCP host (no steering bridge wired)")
	}
	return a.pollRegistry, nil
}

func (a *Adapter) requireBroker() (*broker.Broker, error) {
	if a.broker == nil {
		return nil, errors.New("envelope broker not wired in this MCP host")
	}
	return a.broker, nil
}

func (a *Adapter) handleBrokerSend(ctx context.Context, req map[string]any) (any, error) {
	b, err := a.requireBroker()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	from, err := gomsg.ParseURN(reqStr(req, "from"))
	if err != nil {
		return errResult(ErrCodeArgInvalid, "from: "+err.Error(), "from")
	}
	to, err := gomsg.ParseURN(reqStr(req, "to"))
	if err != nil {
		return errResult(ErrCodeArgInvalid, "to: "+err.Error(), "to")
	}
	env := gomsg.Envelope{
		Kind:        gomsg.Kind(reqStr(req, "kind")),
		From:        from,
		To:          to,
		ThreadID:    reqStr(req, "thread_id"),
		InReplyTo:   reqStr(req, "in_reply_to"),
		Channel:     gomsg.Channel(reqStr(req, "channel")),
		ContentType: reqStr(req, "content_type"),
	}
	if env.ContentType == "" {
		env.ContentType = "application/json"
	}
	if raw := reqStr(req, "payload"); raw != "" {
		env.Payload = json.RawMessage(raw)
	}
	if raw := reqStr(req, "metadata"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &env.Metadata); err != nil {
			return errResult(ErrCodeArgInvalid, "metadata: "+err.Error(), "metadata")
		}
	}
	out, err := b.Send(ctx, env)
	if err != nil {
		return brokerErrResult(err)
	}
	return okResult(out)
}

func (a *Adapter) handleBrokerRequest(ctx context.Context, req map[string]any) (any, error) {
	b, err := a.requireBroker()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	from, err := gomsg.ParseURN(reqStr(req, "from"))
	if err != nil {
		return errResult(ErrCodeArgInvalid, "from: "+err.Error(), "from")
	}
	to, err := gomsg.ParseURN(reqStr(req, "to"))
	if err != nil {
		return errResult(ErrCodeArgInvalid, "to: "+err.Error(), "to")
	}
	timeout := reqInt(req, "timeout_seconds")
	if timeout == 0 {
		timeout = broker.DefaultRequestTimeout
	}
	if timeout < broker.MinRequestTimeout || timeout > broker.MaxRequestTimeout {
		return errResult(ErrCodeArgInvalid, "timeout_seconds out of range", "timeout_seconds")
	}
	env := gomsg.Envelope{
		From:        from,
		To:          to,
		ThreadID:    reqStr(req, "thread_id"),
		Channel:     gomsg.Channel(reqStr(req, "channel")),
		ContentType: reqStr(req, "content_type"),
	}
	if env.ContentType == "" {
		env.ContentType = "application/json"
	}
	if raw := reqStr(req, "payload"); raw != "" {
		env.Payload = json.RawMessage(raw)
	}
	if raw := reqStr(req, "metadata"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &env.Metadata); err != nil {
			return errResult(ErrCodeArgInvalid, "metadata: "+err.Error(), "metadata")
		}
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	resp, err := b.Request(cctx, env)
	if err != nil {
		return brokerErrResult(err)
	}
	return okResult(resp)
}

func (a *Adapter) handleBrokerInbox(ctx context.Context, req map[string]any) (any, error) {
	b, err := a.requireBroker()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	to, err := gomsg.ParseURN(reqStr(req, "to"))
	if err != nil {
		return errResult(ErrCodeArgInvalid, "to: "+err.Error(), "to")
	}
	filter := gomsg.Filter{
		ThreadID: reqStr(req, "thread_id"),
		Limit:    reqInt(req, "limit"),
	}
	if raw := reqStr(req, "kind"); raw != "" {
		for _, k := range splitCSV(raw) {
			filter.Kind = append(filter.Kind, gomsg.Kind(k))
		}
	}
	if raw := reqStr(req, "channel"); raw != "" {
		for _, c := range splitCSV(raw) {
			filter.Channel = append(filter.Channel, gomsg.Channel(c))
		}
	}
	envs, err := b.Inbox(ctx, to, filter)
	if err != nil {
		return brokerErrResult(err)
	}
	return okResult(map[string]interface{}{"envelopes": envs})
}

// handleInboxPoll records a polling opt-in for the caller's address and,
// when a broker is wired, drains its inbox in the same call. The opt-in
// is what makes mid-session polling exclusive with the steering bridge's
// inject-at-turn default: while the opt-in is fresh, the bridge skips
// turn injection for this recipient (see internal/runtime/steering).
func (a *Adapter) handleInboxPoll(ctx context.Context, req map[string]any) (any, error) {
	reg, err := a.requirePollRegistry()
	if err != nil {
		return errResult(ErrCodeDomain, err.Error(), "")
	}
	to, err := gomsg.ParseURN(reqStr(req, "to"))
	if err != nil {
		return errResult(ErrCodeArgInvalid, "to: "+err.Error(), "to")
	}
	// Canonical URN — the exact string the steering bridge keys on as
	// env.To.URN(), so opt-in and bridge-side check agree.
	urn := to.URN()

	release := reqBool(req, "release")
	if release {
		reg.Release(urn)
	} else {
		reg.MarkPolling(urn)
	}

	resp := map[string]interface{}{
		"to":               urn,
		"polling":          !release,
		"poll_ttl_seconds": int(reg.TTL().Seconds()),
	}

	// Draining is a convenience bundled onto the opt-in. An MCP host that
	// has the registry but no broker wired still records the opt-in; the
	// agent then drains through torque_broker_inbox separately.
	if a.broker == nil {
		resp["count"] = 0
		resp["envelopes"] = []gomsg.Envelope{}
		resp["drain_hint"] = "envelope broker not wired on this MCP host — drain via torque_broker_inbox"
		return okResult(resp)
	}

	filter := gomsg.Filter{
		ThreadID: reqStr(req, "thread_id"),
		Limit:    reqInt(req, "limit"),
	}
	if raw := reqStr(req, "kind"); raw != "" {
		for _, k := range splitCSV(raw) {
			filter.Kind = append(filter.Kind, gomsg.Kind(k))
		}
	}
	if raw := reqStr(req, "channel"); raw != "" {
		for _, c := range splitCSV(raw) {
			filter.Channel = append(filter.Channel, gomsg.Channel(c))
		}
	}
	envs, err := a.broker.Inbox(ctx, to, filter)
	if err != nil {
		return brokerErrResult(err)
	}
	resp["count"] = len(envs)
	resp["envelopes"] = envs
	return okResult(resp)
}

// brokerErrResult maps broker / gomsg error sentinels to dual-surface MCP
// error results so callers see the right code (arg_invalid vs domain).
func brokerErrResult(err error) (any, error) {
	switch {
	case errors.Is(err, broker.ErrValidation):
		return errResult(ErrCodeArgInvalid, err.Error(), "")
	case errors.Is(err, broker.ErrPayloadTooLarge):
		return errResult(ErrCodeArgInvalid, err.Error(), "payload")
	case errors.Is(err, gomsg.ErrRequestTimeout):
		return errResult(ErrCodeDomain, err.Error(), "")
	default:
		return errResult(ErrCodeDomain, err.Error(), "")
	}
}

// splitCSV splits a comma-separated string and trims whitespace; empty
// pieces are dropped.
func splitCSV(s string) []string {
	out := make([]string, 0, 4)
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			frag := s[start:i]
			// trim surrounding whitespace
			for len(frag) > 0 && (frag[0] == ' ' || frag[0] == '\t') {
				frag = frag[1:]
			}
			for len(frag) > 0 && (frag[len(frag)-1] == ' ' || frag[len(frag)-1] == '\t') {
				frag = frag[:len(frag)-1]
			}
			if frag != "" {
				out = append(out, frag)
			}
			start = i + 1
		}
	}
	return out
}
