package httpserver

import "net/http"

// WithBuildInfo sets the public build identity returned by /healthz.
// Call it before the listener accepts connections, like WithSecurity.
func (s *Server) WithBuildInfo(version, commit string) *Server {
	if version != "" {
		s.version = version
	}
	if commit != "" {
		s.commit = commit
	}
	return s
}

// healthz reports HTTP liveness, not database or scheduler readiness.
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": s.version,
		"commit":  s.commit,
	})
}
