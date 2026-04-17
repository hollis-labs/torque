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
