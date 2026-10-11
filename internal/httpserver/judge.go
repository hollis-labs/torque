package httpserver

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/torque/internal/judge"
	"github.com/hollis-labs/torque/internal/service"
)

// The suggestion surface (/api/v1/judge/...): tag suggestions from the
// enrichment ledger, accept/reject (accept adds the tags), and the triage
// summary. Reading never spends anything; only enrich calls the decider.

func judgeActor(r *http.Request) string {
	if a := plannerActor(r); a != "" {
		return a
	}
	return "api"
}

func suggestionFilter(r *http.Request) service.SuggestionFilter {
	q := r.URL.Query()
	f := service.SuggestionFilter{TaskID: q.Get("task_id"), SetID: q.Get("set"), QuestionKey: q.Get("question")}
	f.MinConfidence, _ = strconv.ParseFloat(q.Get("min"), 64)
	f.MaxConfidence, _ = strconv.ParseFloat(q.Get("max"), 64)
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	return f
}

func (s *Server) judgeStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.Judge.Status())
}

func (s *Server) judgeSuggestions(w http.ResponseWriter, r *http.Request) {
	out, err := s.svc.Judge.Suggestions(suggestionFilter(r))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) judgeSummary(w http.ResponseWriter, r *http.Request) {
	sets, err := s.svc.Judge.Sets()
	if err != nil {
		writePlannerError(w, err)
		return
	}
	out := map[string]any{}
	for _, id := range judge.SetIDs(sets) {
		counts, err := s.svc.Judge.Counts(id)
		if err != nil {
			writePlannerError(w, err)
			return
		}
		out[id] = counts
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) judgeAccept(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid suggestion id")
		return
	}
	added, err := s.svc.Judge.Accept(id, judgeActor(r))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.sse.Broadcast("judge.changed", map[string]interface{}{"id": id})
	writeJSON(w, http.StatusOK, map[string]any{"accepted": id, "added_tags": added})
}

func (s *Server) judgeReject(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid suggestion id")
		return
	}
	if err := s.svc.Judge.Reject(id, judgeActor(r)); err != nil {
		writePlannerError(w, err)
		return
	}
	s.sse.Broadcast("judge.changed", map[string]interface{}{"id": id})
	writeJSON(w, http.StatusOK, map[string]any{"rejected": id})
}

// judgeBulk decides every open suggestion in a confidence band. The body is
// {"action": "accept"|"reject", "set", "question", "task_id", "min", "max",
// "limit"}; a limit of 1..500 is required.
func (s *Server) judgeBulk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action   string  `json:"action"`
		Set      string  `json:"set"`
		Question string  `json:"question"`
		TaskID   string  `json:"task_id"`
		Min      float64 `json:"min"`
		Max      float64 `json:"max"`
		Limit    int     `json:"limit"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Action != "accept" && body.Action != "reject" {
		writeError(w, http.StatusBadRequest, `action must be "accept" or "reject"`)
		return
	}
	res, err := s.svc.Judge.BulkDecide(service.SuggestionFilter{
		TaskID: body.TaskID, SetID: body.Set, QuestionKey: body.Question,
		MinConfidence: body.Min, MaxConfidence: body.Max, Limit: body.Limit,
	}, body.Action == "accept", judgeActor(r))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.sse.Broadcast("judge.changed", map[string]interface{}{"bulk": true})
	writeJSON(w, http.StatusOK, res)
}

// judgeEnrich asks the installed decider about one task now.
func (s *Server) judgeEnrich(w http.ResponseWriter, r *http.Request) {
	recs, err := s.svc.Judge.EnrichNow(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writePlannerError(w, err)
		return
	}
	s.sse.Broadcast("judge.changed", map[string]interface{}{"task_id": chi.URLParam(r, "id")})
	n := 0
	for _, rec := range recs {
		if len(rec.Suggested) > 0 {
			n++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"answers": len(recs), "suggestions": n})
}
