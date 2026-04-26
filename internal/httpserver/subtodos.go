package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// listSubtodos returns the structural checklist attached to the task. Always
// responds with a `{subtodos: [...]}` envelope and an array body (never null)
// so the client can map over it without a presence guard.
func (s *Server) listSubtodos(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	items, err := s.svc.Task.ListSubtodos(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []sqlstore.Subtodo{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"subtodos": items})
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
