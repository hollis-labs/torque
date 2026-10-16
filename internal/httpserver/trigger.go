package httpserver

import (
	"net/http"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// GUIClientHeader marks a request as made by the Torque GUI (the explorer and
// the Console). Torque's own web client sends X-Client: browser on every
// request; the planner already keys on it. Together with the value below it is
// the only thing that makes a state change a human one.
const (
	GUIClientHeader = "X-Client"
	GUIClientValue  = "browser"
)

// stampHTTPTrigger sets the trigger kind for state changes made through the
// HTTP API (CW-20261016-0133). The kind comes from the surface, not from
// identity (architect ruling torque_trigger_kind_from_surface_2026_10_16): a
// request the GUI marked is human, and an unmarked API call (curl, a script, an
// agent calling REST) is agent. There is no unknown kind. Handlers narrow the ref
// with httpTrigger.
func stampHTTPTrigger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind := sqlstore.TriggerAgent
		if r.Header.Get(GUIClientHeader) == GUIClientValue {
			kind = sqlstore.TriggerHuman
		}
		ctx := sqlstore.ContextWithTrigger(r.Context(), sqlstore.Trigger{Kind: kind, Ref: "http"})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// httpTrigger returns r's context with the trigger's ref set to route, keeping
// the kind the middleware chose.
func httpTrigger(r *http.Request, route string) *http.Request {
	ctx := sqlstore.ContextWithTrigger(r.Context(), sqlstore.TriggerWithRef(r.Context(), "http:"+route))
	return r.WithContext(ctx)
}
