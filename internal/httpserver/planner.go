package httpserver

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/service"
)

// The planner HTTP surface (/api/v1/planner/...): a thin mapping onto
// service.PlannerService, which owns validation, ids, rev and the
// features.planner gate. A disabled feature answers 404, like collections.

// plannerActor names the author of a write when the body does not. The
// explorer GUI sends X-Client: browser; anything else is left to the service
// default (portfolio-manager).
func plannerActor(r *http.Request) string {
	if r.Header.Get("X-Client") == "browser" {
		return "chrispian"
	}
	return ""
}

// writePlannerError maps planner service errors. The body is
// {"error": message, "code": code, "details": {...}}: a validation failure
// carries details.errors (path + message each), a conflict carries
// details.current (the item as it now is).
func writePlannerError(w http.ResponseWriter, err error) {
	var pv *service.PlannerValidationError
	var pc *service.PlannerConflictError
	var ve *service.ValidationError
	var ce *service.ConflictError
	var nf *service.NotFoundError
	var fd *service.FeatureDisabledError
	body := map[string]any{"error": err.Error()}
	status := http.StatusInternalServerError
	switch {
	case errors.As(err, &pv):
		status, body["code"] = http.StatusBadRequest, "invalid"
		body["details"] = map[string]any{"errors": pv.Errors}
	case errors.As(err, &pc):
		status, body["code"] = http.StatusConflict, "conflict"
		if pc.Current != nil {
			body["details"] = map[string]any{"current": pc.Current}
		}
	case errors.As(err, &ve):
		status, body["code"] = http.StatusBadRequest, "invalid"
	case errors.As(err, &ce):
		status, body["code"] = http.StatusConflict, "conflict"
	case errors.As(err, &nf):
		status, body["code"] = http.StatusNotFound, "not_found"
	case errors.As(err, &fd):
		status, body["code"] = http.StatusNotFound, "feature_disabled"
	default:
		body["code"] = "internal"
	}
	writeJSON(w, status, body)
}

func (s *Server) plannerChanged(db, id string) {
	s.sse.Broadcast("planner.changed", map[string]interface{}{"db": db, "id": id})
}

func plannerBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	var body map[string]any
	if err := readJSON(r, &body); err != nil && !errors.Is(err, io.EOF) { // an empty body is an empty object
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return nil, false
	}
	if body == nil {
		body = map[string]any{}
	}
	return body, true
}

func plannerStr(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

// ---- reads ----

func (s *Server) plannerDatabases(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.Planner.Databases()
	if err != nil {
		writePlannerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) plannerSchema(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.Planner.Schema(chi.URLParam(r, "db"))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) plannerSearch(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.Planner.Search(r.URL.Query().Get("q"), r.URL.Query().Get("db"))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// plannerList: every query parameter except sort is an exact-match filter on
// the field of the same name (array fields match membership).
func (s *Server) plannerList(w http.ResponseWriter, r *http.Request) {
	filters := map[string]any{}
	for k, v := range r.URL.Query() {
		if k != "sort" && len(v) > 0 {
			filters[k] = v[0]
		}
	}
	out, err := s.svc.Planner.List(chi.URLParam(r, "db"), filters, r.URL.Query().Get("sort"))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) plannerGet(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.Planner.Get(chi.URLParam(r, "db"), chi.URLParam(r, "id"))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- writes ----

func (s *Server) plannerCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := plannerBody(w, r)
	if !ok {
		return
	}
	db := chi.URLParam(r, "db")
	out, err := s.svc.Planner.Create(db, body, plannerActor(r))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.plannerChanged(db, plannerStr(out, "id"))
	writeJSON(w, http.StatusCreated, out)
}

// plannerUpdate: body {patch, rev?}.
func (s *Server) plannerUpdate(w http.ResponseWriter, r *http.Request) {
	body, ok := plannerBody(w, r)
	if !ok {
		return
	}
	patch, _ := body["patch"].(map[string]any)
	if patch == nil {
		writePlannerError(w, &service.ValidationError{Field: "patch", Message: "patch is required"})
		return
	}
	var rev *int
	if v, present := body["rev"]; present && v != nil {
		f, isNum := v.(float64)
		if !isNum || f != float64(int(f)) {
			writePlannerError(w, &service.ValidationError{Field: "rev", Message: "rev must be an integer"})
			return
		}
		n := int(f)
		rev = &n
	}
	db, id := chi.URLParam(r, "db"), chi.URLParam(r, "id")
	out, err := s.svc.Planner.Update(db, id, patch, rev)
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.plannerChanged(db, id)
	writeJSON(w, http.StatusOK, out)
}

// plannerComment: body {text, author?, kind?}.
func (s *Server) plannerComment(w http.ResponseWriter, r *http.Request) {
	body, ok := plannerBody(w, r)
	if !ok {
		return
	}
	db, id := chi.URLParam(r, "db"), chi.URLParam(r, "id")
	out, err := s.svc.Planner.Comment(db, id, plannerStr(body, "text"), plannerStr(body, "author"), plannerStr(body, "kind"), plannerActor(r))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.plannerChanged(db, id)
	writeJSON(w, http.StatusOK, out)
}

func plannerRefOf(v any) service.PlannerRef {
	m, _ := v.(map[string]any)
	return service.PlannerRef{DB: plannerStr(m, "db"), ID: plannerStr(m, "id")}
}

// plannerRelate: body {from:{db,id}, to:{db,id}, field?}; POST adds,
// DELETE removes. Item-to-item references; external pointers use /links.
func (s *Server) plannerRelate(add bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := plannerBody(w, r)
		if !ok {
			return
		}
		from, to := plannerRefOf(body["from"]), plannerRefOf(body["to"])
		out, err := s.svc.Planner.Link(from, to, plannerStr(body, "field"), add)
		if err != nil {
			writePlannerError(w, err)
			return
		}
		s.plannerChanged(from.DB, from.ID)
		writeJSON(w, http.StatusOK, out)
	}
}

// plannerLinkAdd: body {link:{kind, ref, label?}}.
func (s *Server) plannerLinkAdd(w http.ResponseWriter, r *http.Request) {
	body, ok := plannerBody(w, r)
	if !ok {
		return
	}
	l, _ := body["link"].(map[string]any)
	if l == nil {
		writePlannerError(w, &service.ValidationError{Field: "link", Message: "link must be an object {kind, ref, label?}"})
		return
	}
	db, id := chi.URLParam(r, "db"), chi.URLParam(r, "id")
	out, err := s.svc.Planner.LinkAdd(db, id, service.PlannerLink{Kind: plannerStr(l, "kind"), Ref: plannerStr(l, "ref"), Label: plannerStr(l, "label")})
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.plannerChanged(db, id)
	writeJSON(w, http.StatusOK, out)
}

// plannerLinkRemove: body {kind, ref}.
func (s *Server) plannerLinkRemove(w http.ResponseWriter, r *http.Request) {
	body, ok := plannerBody(w, r)
	if !ok {
		return
	}
	db, id := chi.URLParam(r, "db"), chi.URLParam(r, "id")
	out, err := s.svc.Planner.LinkRemove(db, id, plannerStr(body, "kind"), plannerStr(body, "ref"))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.plannerChanged(db, id)
	writeJSON(w, http.StatusOK, out)
}

// plannerReorder: body {ids:[...]}.
func (s *Server) plannerReorder(w http.ResponseWriter, r *http.Request) {
	body, ok := plannerBody(w, r)
	if !ok {
		return
	}
	raw, _ := body["ids"].([]any)
	ids := make([]string, 0, len(raw))
	for _, v := range raw {
		id, isStr := v.(string)
		if !isStr {
			writePlannerError(w, &service.ValidationError{Field: "ids", Message: "ids must be an array of strings"})
			return
		}
		ids = append(ids, id)
	}
	db := chi.URLParam(r, "db")
	out, err := s.svc.Planner.Reorder(db, ids)
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.plannerChanged(db, "")
	writeJSON(w, http.StatusOK, out)
}

// plannerDecide: body {option, comment?, author?}.
func (s *Server) plannerDecide(w http.ResponseWriter, r *http.Request) {
	body, ok := plannerBody(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	out, err := s.svc.Planner.Decide(id, plannerStr(body, "option"), plannerStr(body, "comment"), plannerStr(body, "author"), plannerActor(r))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.plannerChanged("decisions", id)
	writeJSON(w, http.StatusOK, out)
}

// plannerDefer: body {note?, author?}.
func (s *Server) plannerDefer(w http.ResponseWriter, r *http.Request) {
	body, ok := plannerBody(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	out, err := s.svc.Planner.Defer(id, plannerStr(body, "note"), plannerStr(body, "author"), plannerActor(r))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.plannerChanged("decisions", id)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) plannerReopen(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	out, err := s.svc.Planner.Reopen(id)
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.plannerChanged("decisions", id)
	writeJSON(w, http.StatusOK, out)
}

// plannerInboxAdd: body is the inbox item ({title, kind?, body?, path?, added_by?}).
func (s *Server) plannerInboxAdd(w http.ResponseWriter, r *http.Request) {
	body, ok := plannerBody(w, r)
	if !ok {
		return
	}
	out, err := s.svc.Planner.InboxAdd(body, plannerStr(body, "added_by"), plannerActor(r))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.plannerChanged("inbox", plannerStr(out, "id"))
	writeJSON(w, http.StatusCreated, out)
}

// plannerInboxPromote: body {to_db, fields?}.
func (s *Server) plannerInboxPromote(w http.ResponseWriter, r *http.Request) {
	body, ok := plannerBody(w, r)
	if !ok {
		return
	}
	fields, _ := body["fields"].(map[string]any)
	id, toDB := chi.URLParam(r, "id"), plannerStr(body, "to_db")
	inbox, item, err := s.svc.Planner.InboxPromote(id, toDB, fields, plannerActor(r))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.plannerChanged("inbox", id)
	s.plannerChanged(toDB, plannerStr(item, "id"))
	writeJSON(w, http.StatusOK, map[string]any{"inbox": inbox, "item": item})
}

// plannerInboxDismiss: body {note?, author?}.
func (s *Server) plannerInboxDismiss(w http.ResponseWriter, r *http.Request) {
	body, ok := plannerBody(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	out, err := s.svc.Planner.InboxDismiss(id, plannerStr(body, "note"), plannerStr(body, "author"), plannerActor(r))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.plannerChanged("inbox", id)
	writeJSON(w, http.StatusOK, out)
}

// plannerBoard: GET /planner/board?recent_hours=&active_hours=&limit=
func (s *Server) plannerBoard(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var opts service.PlannerBoardOptions
	for _, f := range []struct {
		name string
		dst  *float64
	}{{"recent_hours", &opts.RecentHours}, {"active_hours", &opts.ActiveHours}} {
		if v := q.Get(f.name); v != "" {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				writePlannerError(w, &service.ValidationError{Field: f.name, Message: f.name + " must be a number"})
				return
			}
			*f.dst = n
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writePlannerError(w, &service.ValidationError{Field: "limit", Message: "limit must be an integer"})
			return
		}
		opts.Limit = n
	}
	out, err := s.svc.Planner.Board(opts)
	if err != nil {
		writePlannerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// plannerTaskTitles: GET /planner/torque-titles?ids=a,b,c -> {id: {id,title,status,priority,updated_at} | null}
func (s *Server) plannerTaskTitles(w http.ResponseWriter, r *http.Request) {
	var ids []string
	for _, id := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			ids = append(ids, id)
		}
	}
	out, err := s.svc.Planner.TaskTitles(ids)
	if err != nil {
		writePlannerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// plannerWorkstreamTasks: GET /planner/workstreams/{id}/tasks
func (s *Server) plannerWorkstreamTasks(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.Planner.WorkstreamTasks(chi.URLParam(r, "id"))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
