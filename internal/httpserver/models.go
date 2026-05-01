package httpserver

import (
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
	ProviderID      string                  `json:"provider_id"`
	ID              string                  `json:"id"`
	Name            string                  `json:"name"`
	Family          string                  `json:"family"`
	OpenWeights     bool                    `json:"open_weights"`
	ReleaseDate     string                  `json:"release_date"`
	KnowledgeCutoff string                  `json:"knowledge_cutoff"`
	LastUpdated     string                  `json:"last_updated"`
	Cost            modelsdev.Pricing       `json:"cost"`
	Limit           modelsdev.Limits        `json:"limit"`
	Modality        modelsdev.Modality      `json:"modality"`
	Capabilities    modelsdev.Capabilities  `json:"capabilities"`
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
	if s.svc.Models == nil {
		writeJSON(w, http.StatusOK, map[string]any{"models": []any{}})
		return
	}
	all := s.svc.Models.List()
	providerFilter := strings.TrimSpace(r.URL.Query().Get("provider"))
	out := make([]modelEntry, 0, len(all))
	for _, m := range all {
		if providerFilter != "" && m.ProviderID != providerFilter {
			continue
		}
		out = append(out, toModelEntry(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out})
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
