package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/hollis-labs/torque/internal/hitl"
)

func (s *Server) listHITLWorkflows(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"workflows": hitl.ListCanonical(),
	})
}

func (s *Server) getHITLWorkflow(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, hitl.Lookup(chi.URLParam(r, "type")))
}
