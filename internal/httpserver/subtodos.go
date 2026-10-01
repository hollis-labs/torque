package httpserver

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service/pagination"
)

// listSubtodos reads a bounded position-ordered page of the task checklist.
func (s *Server) listSubtodos(w http.ResponseWriter, r *http.Request) {
	values, err := parseStrictQuery(r, map[string]bool{"limit": true, "cursor": true, "sort_by": true, "sort_dir": true, "include_total": true})
	if err != nil {
		writeHTTPQueryError(w, err)
		return
	}
	q, err := queryCursor(values)
	if err != nil {
		writeHTTPQueryError(w, err)
		return
	}
	page, queryErr := s.svc.Task.QuerySubtodos(chi.URLParam(r, "id"), q)
	if queryErr != nil {
		writeAdjacentServiceError(w, queryErr)
		return
	}
	more := len(page.Items) > page.Query.Limit
	if more {
		page.Items = page.Items[:page.Query.Limit]
	}
	var next *string
	if more && len(page.Items) > 0 {
		i := len(page.Items) - 1
		v := pagination.Encode(page.Query.SortBy, page.Query.SortDir, strconv.Itoa(page.Positions[i]), page.Items[i].ID)
		next = &v
	}
	meta := pagination.NewPageMeta(len(page.Items), page.Query.Limit, more, next, page.Total, nil)
	writeJSON(w, http.StatusOK, map[string]any{"items": page.Items, "meta": struct {
		pagination.PageMeta
		SortBy  string `json:"sort_by"`
		SortDir string `json:"sort_dir"`
	}{meta, page.Query.SortBy, page.Query.SortDir}})
}

// addSubtodo appends a new checklist item. The id must be unique within the
// task; duplicate ids are rejected.
func (s *Server) addSubtodo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		ID       string `json:"id"`
		Text     string `json:"text"`
		Required bool   `json:"required"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	items, err := s.svc.Task.AddSubtodo(id, sqlstore.Subtodo{
		ID:       body.ID,
		Text:     body.Text,
		Required: body.Required,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if items == nil {
		items = []sqlstore.Subtodo{}
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{"subtodos": items})
}

// markSubtodoDone ticks a single checklist item with an optional evidence
// string and returns the updated list. Mirrors the MCP tool semantics — same
// service call the executor hits via stdio.
func (s *Server) markSubtodoDone(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	itemID := chi.URLParam(r, "item_id")
	var body struct {
		Evidence string `json:"evidence"`
	}
	// Empty body is legal — evidence is optional. Only fail on truly
	// malformed JSON, not missing fields.
	if r.ContentLength > 0 {
		if err := readJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
	}
	items, err := s.svc.Task.MarkSubtodoDone(id, itemID, body.Evidence)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if items == nil {
		items = []sqlstore.Subtodo{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"subtodos": items})
}

// updateSubtodo edits the text and/or required flag on a checklist item.
// Both fields are optional in the body; omitted fields leave the existing
// value untouched. Returns the updated list on success.
func (s *Server) updateSubtodo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	itemID := chi.URLParam(r, "item_id")
	var body struct {
		Text     *string `json:"text"`
		Required *bool   `json:"required"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	items, err := s.svc.Task.UpdateSubtodo(id, itemID, body.Text, body.Required)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if items == nil {
		items = []sqlstore.Subtodo{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"subtodos": items})
}

// deleteSubtodo removes a checklist item. Returns the updated list (which
// may be empty).
func (s *Server) deleteSubtodo(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	itemID := chi.URLParam(r, "item_id")
	items, err := s.svc.Task.DeleteSubtodo(id, itemID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if items == nil {
		items = []sqlstore.Subtodo{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"subtodos": items})
}
