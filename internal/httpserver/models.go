package httpserver

import (
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/go-modelsdev/modelsdev"
)

// modelEntry mirrors modelsdev.ModelRef with explicit snake_case json tags.
// Upstream ModelRef intentionally has no tags (fields would render CamelCase),
// which would break consumers expecting the same shape as Model. Wrapping is
// cheaper than a fork.
type modelEntry struct {
	ProviderID      string                 `json:"provider_id"`
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	Family          string                 `json:"family"`
	OpenWeights     bool                   `json:"open_weights"`
	ReleaseDate     string                 `json:"release_date"`
	KnowledgeCutoff string                 `json:"knowledge_cutoff"`
	LastUpdated     string                 `json:"last_updated"`
	Cost            modelsdev.Pricing      `json:"cost"`
	Limit           modelsdev.Limits       `json:"limit"`
	Modality        modelsdev.Modality     `json:"modality"`
	Capabilities    modelsdev.Capabilities `json:"capabilities"`
}

func toModelEntry(m modelsdev.ModelRef) modelEntry {
	return modelEntry{
		ProviderID:      m.ProviderID,
		ID:              m.ID,
		Name:            m.Name,
		Family:          m.Family,
		OpenWeights:     m.OpenWeights,
		ReleaseDate:     m.ReleaseDate,
		KnowledgeCutoff: m.KnowledgeCutoff,
		LastUpdated:     m.LastUpdated,
		Cost:            m.Cost,
		Limit:           m.Limit,
		Modality:        m.Modality,
		Capabilities:    m.Capabilities,
	}
}

func toModelEntryFromModel(providerID string, m modelsdev.Model) modelEntry {
	return modelEntry{
		ProviderID:      providerID,
		ID:              m.ID,
		Name:            m.Name,
		Family:          m.Family,
		OpenWeights:     m.OpenWeights,
		ReleaseDate:     m.ReleaseDate,
		KnowledgeCutoff: m.KnowledgeCutoff,
		LastUpdated:     m.LastUpdated,
		Cost:            m.Cost,
		Limit:           m.Limit,
		Modality:        m.Modality,
		Capabilities:    m.Capabilities,
	}
}

// listModels returns every (provider, model) pair currently known to the
// catalog. Optional query filter `provider=<id>` narrows to a single provider.
// On a cold cache (refresher hasn't fetched yet) the response is `{models: []}`
// with HTTP 200 — clients should retry rather than treat absence as fatal.
func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	q, e := parseStrictQuery(r, map[string]bool{"provider": true, "search": true, "limit": true, "sort_by": true, "sort_dir": true, "cursor": true, "offset": true, "include_total": true})
	if e != nil {
		writeHTTPQueryError(w, e)
		return
	}
	c, e := queryCursor(q)
	if e != nil {
		writeHTTPQueryError(w, e)
		return
	}
	rq := service.ResourceQuery{CursorQuery: c}
	if _, ok := q["offset"]; ok {
		v, e := queryInt(q, "offset")
		if e != nil {
			writeHTTPQueryError(w, e)
			return
		}
		rq.Offset = &v
	}
	page, err := s.svc.ListModelPage(strings.TrimSpace(queryString(q, "provider")), queryString(q, "search"), rq)
	if err != nil {
		writeAdjacentServiceError(w, err)
		return
	}
	more := len(page.Rows) > page.Query.Limit
	if more {
		page.Rows = page.Rows[:page.Query.Limit]
	}
	items := make([]modelEntry, 0, len(page.Rows))
	for _, row := range page.Rows {
		items = append(items, toModelEntry(row.Record.(modelsdev.ModelRef)))
	}
	next := ""
	if more && len(page.Rows) > 0 {
		last := page.Rows[len(page.Rows)-1]
		next = pagination.Encode(page.Query.SortBy, page.Query.SortDir, last.SortValue, last.ID)
	}
	writeJSON(w, 200, map[string]any{"items": items, "meta": advancedPageMeta(page.Query.Limit, page.Query.SortBy, page.Query.SortDir, more, next, len(items), page.Total, page.Offset)})
}

// getModel returns metadata for a single (provider, model) pair. Returns 404
// when the pair is unknown OR the catalog hasn't loaded yet — the response
// includes a hint so clients can distinguish cold-cache from genuine absence.
func (s *Server) getModel(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	model := chi.URLParam(r, "model")
	if s.svc.Models == nil {
		writeError(w, http.StatusNotFound, "model catalog not initialized")
		return
	}
	m, ok := s.svc.Models.Get(provider, model)
	if !ok {
		writeError(w, http.StatusNotFound, "model not found (cold cache or unknown id)")
		return
	}
	writeJSON(w, http.StatusOK, toModelEntryFromModel(provider, m))
}
