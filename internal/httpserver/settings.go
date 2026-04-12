package httpserver

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) getAllSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.svc.Settings.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) getSetting(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	val, err := s.svc.Settings.Get(key)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": val})
}

func (s *Server) saveSetting(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	var req struct {
		Value string `json:"value"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := s.svc.Settings.Set(key, req.Value); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": req.Value})
}

func (s *Server) saveAllSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	for k, v := range req {
		if err := s.svc.Settings.Set(k, v); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) schedulerStatus(w http.ResponseWriter, r *http.Request) {
	if s.sched == nil {
		writeError(w, http.StatusServiceUnavailable, "scheduler not running")
		return
	}
	writeJSON(w, http.StatusOK, s.sched.Status())
}

func (s *Server) schedulerToggle(w http.ResponseWriter, r *http.Request) {
	if s.sched == nil {
		writeError(w, http.StatusServiceUnavailable, "scheduler not running")
		return
	}
	status := s.sched.Status()
	s.sched.SetEnabled(!status.Enabled)
	writeJSON(w, http.StatusOK, s.sched.Status())
}

func (s *Server) getFeatures(w http.ResponseWriter, r *http.Request) {
	sprints, _ := s.svc.Settings.Get("features.sprints")
	projects, _ := s.svc.Settings.Get("features.projects")
	epics, _ := s.svc.Settings.Get("features.epics")
	writeJSON(w, http.StatusOK, map[string]bool{
		"sprints":  sprints == "true",
		"projects": projects == "true",
		"epics":    epics == "true",
	})
}

func (s *Server) getPluginUI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"components": []interface{}{}})
}
