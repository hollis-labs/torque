package httpserver

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
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
	if r.URL.RawQuery != "" {
		s.listTagsPage(w, r)
		return
	}
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

func (s *Server) listTagsPage(w http.ResponseWriter, r *http.Request) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeFieldError(w, http.StatusBadRequest, "query", "invalid query string: "+err.Error())
		return
	}
	if qerr := validateTagListQueryKeys(q); qerr != nil {
		writeFieldError(w, http.StatusBadRequest, qerr.field, qerr.Error())
		return
	}
	limit := 0
	if _, ok := q["limit"]; ok {
		n, err := parseTagListInt(q.Get("limit"), "limit")
		if err != nil {
			writeFieldError(w, http.StatusBadRequest, "limit", err.Error())
			return
		}
		limit = n
	}
	afterName, afterSlug, err := service.DecodeTagListCursor(q.Get("cursor"))
	if err != nil {
		var verr *service.ValidationError
		if errors.As(err, &verr) {
			writeFieldError(w, http.StatusBadRequest, verr.Field, verr.Message)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	result, err := s.svc.Tag.ListPage(service.TagListInput{
		Query:     q.Get("query"),
		Color:     q.Get("color"),
		Limit:     limit,
		AfterName: afterName,
		AfterSlug: afterSlug,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tags := result.Tags
	if tags == nil {
		tags = []sqlstore.TagRecord{}
	}
	var nextCursor interface{}
	if result.HasMoreFromQuery && len(tags) > 0 {
		nextCursor = service.TagListCursor(tags[len(tags)-1])
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tags":        tagsJSON(tags),
		"total":       result.Total,
		"returned":    len(tags),
		"limit":       result.Limit,
		"has_more":    result.HasMoreFromQuery,
		"next_cursor": nextCursor,
		"sort_by":     service.TagListSortBy,
		"sort_dir":    service.TagListSortDir,
	})
}

type tagListQueryError struct {
	field   string
	message string
}

func (e tagListQueryError) Error() string { return e.message }

func validateTagListQueryKeys(q url.Values) *tagListQueryError {
	supported := map[string]bool{
		"query": true, "color": true, "limit": true, "cursor": true,
	}
	for key, values := range q {
		if !supported[key] {
			return &tagListQueryError{
				field:   key,
				message: "unsupported query parameter " + key + "; supported tag-list parameters are query, color, limit, cursor",
			}
		}
		if len(values) > 1 {
			return &tagListQueryError{
				field:   key,
				message: "query parameter " + key + " may only be supplied once",
			}
		}
	}
	return nil
}

func parseTagListInt(raw, field string) (int, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, fmt.Errorf("%s must be an integer", field)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", field)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be greater than 0", field)
	}
	return n, nil
}

func (s *Server) getTag(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	tag, err := s.svc.Tag.Get(slug)
	if err != nil {
		if errors.Is(err, sqlstore.ErrTagNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
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
		if service.IsTagUniqueConstraintError(err) {
			writeError(w, http.StatusConflict, "tag slug already exists")
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
		if errors.Is(err, sqlstore.ErrTagNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tagJSON(tag))
}

func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if err := s.svc.Tag.Delete(slug); err != nil {
		if errors.Is(err, sqlstore.ErrTagNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
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
		if errors.Is(err, sqlstore.ErrTagNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	dest, err := s.svc.Tag.Get(req.Into)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tagJSON(dest))
}
