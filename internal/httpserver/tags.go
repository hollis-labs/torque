package httpserver

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

// tagJSON converts a TagRecord to a JSON-friendly map.
func tagJSON(t *sqlstore.TagRecord) map[string]interface{} {
	return map[string]interface{}{
		"slug":        t.Slug,
		"name":        t.Name,
		"description": t.Description,
		"color":       t.Color,
		"created_at":  t.CreatedAt.Format(time.RFC3339),
		"updated_at":  t.UpdatedAt.Format(time.RFC3339),
	}
}

func tagsJSON(tags []sqlstore.TagRecord) []map[string]interface{} {
	out := make([]map[string]interface{}, len(tags))
	for i := range tags {
		out[i] = tagJSON(&tags[i])
	}
	return out
}

func (s *Server) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.svc.Tag.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tags == nil {
		tags = []sqlstore.TagRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tags": tagsJSON(tags)})
}

func (s *Server) getTag(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	tag, err := s.svc.Tag.Get(slug)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tagJSON(tag))
}

func (s *Server) createTag(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
		Color       string `json:"color"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	tag, err := s.svc.Tag.Create(service.TagCreateInput{
		Name:        req.Name,
		Slug:        req.Slug,
		Description: req.Description,
		Color:       req.Color,
	})
	if err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, tagJSON(tag))
}

func (s *Server) updateTag(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var req struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Color       *string `json:"color"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	tag, err := s.svc.Tag.Update(slug, service.TagUpdateInput{
		Name:        req.Name,
		Description: req.Description,
		Color:       req.Color,
	})
	if err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		// Store layer returns "tag X not found" — translate to 404
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tagJSON(tag))
}

func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if err := s.svc.Tag.Delete(slug); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mergeTags(w http.ResponseWriter, r *http.Request) {
	sourceSlug := chi.URLParam(r, "slug")
	var req struct {
		Into string `json:"into"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Into == "" {
		writeError(w, http.StatusUnprocessableEntity, "'into' is required")
		return
	}

	if err := s.svc.Tag.Merge(sourceSlug, req.Into); err != nil {
		if _, ok := err.(*service.ValidationError); ok {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	dest, err := s.svc.Tag.Get(req.Into)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tagJSON(dest))
}
