package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

// Server holds the router, service layer, scheduler reference, and SSE hub.
type Server struct {
	svc    *service.Service
	sched  *scheduler.Scheduler
	router chi.Router
	sse    *SSEHub
}

// New constructs an HTTP server with routes registered.
// sched may be nil — handlers that need it will return 503.
func New(svc *service.Service, sched *scheduler.Scheduler) *Server {
	s := &Server{
		svc:    svc,
		sched:  sched,
		router: chi.NewRouter(),
		sse:    NewSSEHub(),
	}
	s.routes()
	return s
}

// ServeHTTP implements http.Handler by delegating to the internal chi router.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// SSEHub returns the server's SSE hub. Used by the scheduler event bridge
// at startup to forward bus events to connected SSE clients.
func (s *Server) SSEHub() *SSEHub {
	return s.sse
}

func (s *Server) routes() {
	r := s.router

	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(corsMiddleware)

	r.Route("/api/v1", func(r chi.Router) {
		// Tasks
		r.Get("/tasks", s.listTasks)
		r.Post("/tasks", s.createTask)
		r.Get("/tasks/search", s.searchTasks)
		r.Post("/tasks/bulk-transition", s.bulkTransitionTasks)
		r.Get("/tasks/{id}", s.getTask)
		r.Put("/tasks/{id}", s.updateTask)
		r.Delete("/tasks/{id}", s.deleteTask)
		r.Post("/tasks/{id}/transition", s.transitionTask)
		r.Get("/tasks/{id}/checkpoints", s.listTaskCheckpoints)
		r.Get("/tasks/{id}/comments", s.listComments)
		r.Post("/tasks/{id}/comments", s.addComment)
		r.Get("/tasks/{id}/artifacts", s.listArtifacts)
		r.Get("/tasks/{id}/subtodos", s.listSubtodos)
		r.Post("/tasks/{id}/subtodos/{item_id}/done", s.markSubtodoDone)

		// Checkpoints — emit/respond/cancel keyed on correlation_id.
		r.Post("/checkpoints", s.emitCheckpoint)
		r.Get("/checkpoints/pending", s.listPendingCheckpoints)
		r.Get("/checkpoints/{correlation_id}", s.getCheckpoint)
		r.Post("/checkpoints/{correlation_id}/respond", s.respondCheckpoint)
		r.Post("/checkpoints/{correlation_id}/cancel", s.cancelCheckpoint)

		// Templates — versioned task factories.
		r.Get("/templates", s.listTemplates)
		r.Post("/templates", s.createTemplate)
		r.Get("/templates/{id}", s.getTemplate)
		r.Put("/templates/{id}", s.updateTemplate)
		r.Delete("/templates/{id}", s.deleteTemplate)
		r.Post("/templates/{id}/archive/{version}", s.archiveTemplate)
		r.Post("/templates/{id}/instantiate", s.instantiateTemplate)

		// Projects
		r.Get("/projects", s.listProjects)
		r.Post("/projects", s.createProject)
		r.Get("/projects/{id}", s.getProject)
		r.Put("/projects/{id}", s.updateProject)
		r.Delete("/projects/{id}", s.deleteProject)

		// Sprints
		r.Get("/sprints", s.listSprints)
		r.Post("/sprints", s.createSprint)
		r.Get("/sprints/{id}", s.getSprint)
		r.Put("/sprints/{id}", s.updateSprint)
		r.Delete("/sprints/{id}", s.deleteSprint)
		r.Post("/sprints/{id}/transition", s.transitionSprint)

		// Epics
		r.Get("/epics", s.listEpics)
		r.Post("/epics", s.createEpic)
		r.Get("/epics/{id}", s.getEpic)
		r.Put("/epics/{id}", s.updateEpic)
		r.Delete("/epics/{id}", s.deleteEpic)

		// Plans — convenience endpoints over kind=plan tasks.
		r.Get("/plans", s.listPlans)
		r.Post("/plans", s.createPlan)
		r.Get("/plans/{id}", s.getPlan)
		r.Post("/plans/{id}/phases", s.addPlanPhase)
		r.Delete("/plans/{id}/phases/{phase_id}", s.removePlanPhase)
		r.Get("/plans/{id}/children", s.listPlanChildren)

		// Tags
		r.Get("/tags", s.listTags)
		r.Post("/tags", s.createTag)
		r.Get("/tags/{slug}", s.getTag)
		r.Patch("/tags/{slug}", s.updateTag)
		r.Delete("/tags/{slug}", s.deleteTag)
		r.Post("/tags/{slug}/merge", s.mergeTags)

		// Runs
		r.Get("/runs", s.listRuns)
		r.Get("/runs/{id}", s.getRun)

		// Artifacts
		r.Get("/artifacts", s.listArtifacts)
		r.Post("/artifacts", s.createArtifact)
		r.Get("/artifacts/{id}", s.getArtifact)
		r.Delete("/artifacts/{id}", s.deleteArtifact)
		r.Get("/artifacts/{id}/content", s.serveArtifactContent)

		// Comments
		r.Get("/comments", s.listComments)
		r.Post("/comments", s.addComment)

		// Settings
		r.Get("/settings", s.getAllSettings)
		r.Put("/settings", s.saveAllSettings)
		r.Get("/settings/{key}", s.getSetting)
		r.Put("/settings/{key}", s.saveSetting)

		// Scheduler
		r.Get("/scheduler/status", s.schedulerStatus)
		r.Post("/scheduler/toggle", s.schedulerToggle)

		// Features
		r.Get("/features", s.getFeatures)

		// Admin — localhost-only, optional X-Admin-Token gate.
		r.Route("/admin", func(r chi.Router) {
			r.Post("/restart-frontend", adminGate(s.restartFrontend))
		})

		// Plugin UI (stub)
		r.Get("/plugins/ui", s.getPluginUI)

		// SSE
		r.Get("/events", s.sse.ServeHTTP)
	})

	// SPA fallback
	r.NotFound(spaHandler())
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}
