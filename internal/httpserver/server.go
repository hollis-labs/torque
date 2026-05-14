package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	gomsg "github.com/hollis-labs/go-messaging"

	"github.com/hollis-labs/torque/internal/broker"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/service"
)

// Server holds the router, service layer, scheduler reference, and SSE hub.
type Server struct {
	svc    *service.Service
	sched  *scheduler.Scheduler
	msg    gomsg.Store // optional; routes 503 when nil. Set via SetMessaging.
	router chi.Router
	sse    *SSEHub
	// sessions is the unified agent session manager (CW-20260508-0001 —
	// replaces sessionmgr.Manager). Nil disables /api/v1/sessions/* — the
	// routes return 503 in that mode rather than panic, mirroring sched=nil
	// behavior.
	sessions *agent.Manager
	// broker is the typed envelope dispatcher (CW-20260503-0013, S1.3).
	// Wired via SetBroker; /api/v1/broker/* routes 503 when nil.
	broker *broker.Broker
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

// WithSessions attaches the unified agent session manager so the
// /api/v1/sessions/* routes serve real data. Safe to call before the
// listener accepts connections; goroutine-unsafe under live traffic.
func (s *Server) WithSessions(mgr *agent.Manager) *Server {
	s.sessions = mgr
	return s
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
		r.Post("/tasks/{id}/subtodos", s.addSubtodo)
		r.Post("/tasks/{id}/subtodos/{item_id}/done", s.markSubtodoDone)
		r.Patch("/tasks/{id}/subtodos/{item_id}", s.updateSubtodo)
		r.Delete("/tasks/{id}/subtodos/{item_id}", s.deleteSubtodo)

		// Issues — project-scoped backlog capture rows over kind=issue tasks.
		r.Get("/issues", s.listIssues)
		r.Post("/issues", s.createIssue)
		r.Get("/issues/search", s.searchIssues)
		r.Get("/issues/{id}", s.getIssue)
		r.Put("/issues/{id}", s.updateIssue)

		// Checkpoints — emit/respond/cancel keyed on correlation_id.
		r.Get("/checkpoint-workflows", s.listHITLWorkflows)
		r.Get("/checkpoint-workflows/{type}", s.getHITLWorkflow)
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
		r.Get("/projects/{id}/artifacts", s.listProjectArtifacts)
		r.Post("/projects/{id}/artifacts", s.createProjectArtifact)
		r.Put("/projects/{id}/artifacts/{artifactID}", s.updateProjectArtifact)
		r.Delete("/projects/{id}/artifacts/{artifactID}", s.deleteProjectArtifact)

		// Sprints
		r.Get("/sprints", s.listSprints)
		r.Post("/sprints", s.createSprint)
		r.Get("/sprints/{id}", s.getSprint)
		r.Put("/sprints/{id}", s.updateSprint)
		r.Delete("/sprints/{id}", s.deleteSprint)
		r.Post("/sprints/{id}/transition", s.transitionSprint)

		// Collections — kanban-style task containers + an inbox view.
		// Task move/inbox sub-routes are intentionally registered before
		// the parameterized {id} routes so the chi router doesn't try to
		// pattern-match "tasks" or "inbox" as a collection ID.
		r.Get("/collections/inbox/tasks", s.listInboxTasks)
		r.Post("/collections/inbox/tasks", s.addTaskToInbox)
		r.Post("/collections/tasks/move", s.moveTaskToCollection)
		r.Get("/collections", s.listCollections)
		r.Post("/collections", s.createCollection)
		r.Get("/collections/{id}", s.getCollection)
		r.Put("/collections/{id}", s.updateCollection)
		r.Post("/collections/{id}/archive", s.archiveCollection)
		r.Post("/collections/{id}/unarchive", s.unarchiveCollection)
		r.Get("/collections/{id}/tasks", s.listCollectionTasks)
		r.Post("/collections/{id}/tasks", s.addTaskToCollection)
		r.Delete("/collections/{id}/tasks/{task_id}", s.removeTaskFromCollection)
		r.Put("/collections/{id}/tasks/order", s.reorderCollectionTasks)

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
		r.Post("/plans/{id}/start", s.startPlan)

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
		r.Get("/settings/feature-flags", s.getFeatures)
		r.Get("/settings/{key}", s.getSetting)
		r.Put("/settings/{key}", s.saveSetting)

		// Scheduler
		r.Get("/scheduler/status", s.schedulerStatus)
		r.Post("/scheduler/toggle", s.schedulerToggle)

		// Sessions — long-lived agent sessions (sessionmgr).
		r.Get("/sessions", s.listSessions)
		r.Post("/sessions/launch", s.launchSession)
		r.Get("/sessions/{id}", s.getSession)
		r.Post("/sessions/{id}/stop", s.stopSession)
		r.Post("/sessions/{id}/wait", s.waitSession)
		r.Post("/sessions/{id}/resize", s.resizeSession)
		r.Post("/sessions/{id}/checkpoint", s.checkpointSession)
		r.Get("/sessions/{id}/checkpoints", s.listSessionCheckpoints)
		r.Post("/sessions/{id}/resume", s.resumeSession)

		// Models — go-modelsdev catalog. Cold cache returns empty list / 404
		// so callers can retry rather than treat absence as fatal.
		r.Get("/models", s.listModels)
		r.Get("/models/{provider}/{model}", s.getModel)

		// Features
		r.Get("/features", s.getFeatures)

		// Admin — localhost-only, optional X-Admin-Token gate.
		r.Route("/admin", func(r chi.Router) {
			r.Post("/restart-frontend", adminGate(s.restartFrontend))
		})

		// Plugin UI (stub)
		r.Get("/plugins/ui", s.getPluginUI)

		// Messages — go-messaging Store contract over SQLite (S1.2). Routes
		// always register; handlers 503 when no Store has been wired via
		// Server.SetMessaging.
		r.Post("/messages", s.sendMessage)
		r.Get("/messages/inbox", s.listInbox)
		r.Get("/messages/subscribe", s.subscribeMessages)
		r.Get("/messages/thread/{thread_id}", s.listThread)
		r.Get("/messages/{id}", s.getMessage)
		r.Post("/messages/{id}/cancel", s.cancelMessage)
		r.Post("/messages/{id}/consume", s.consumeMessage)

		// Broker — typed envelope dispatcher (S1.3). 503 when no broker is
		// wired via Server.SetBroker.
		r.Post("/broker/send", s.brokerSend)
		r.Post("/broker/request", s.brokerRequest)
		r.Get("/broker/inbox", s.brokerInbox)

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
