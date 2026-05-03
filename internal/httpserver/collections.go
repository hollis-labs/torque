package httpserver

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

func collectionJSON(c *sqlstore.CollectionRecord) map[string]interface{} {
	return map[string]interface{}{
		"id":          c.ID,
		"name":        c.Name,
		"description": c.Description,
		"archived_at": nullTime(c.ArchivedAt),
		"created_at":  c.CreatedAt,
		"updated_at":  c.UpdatedAt,
	}
}

func collectionsJSON(collections []sqlstore.CollectionRecord) []map[string]interface{} {
	out := make([]map[string]interface{}, len(collections))
	for i := range collections {
		out[i] = collectionJSON(&collections[i])
	}
	return out
}

// writeCollectionError maps service errors to HTTP statuses. Mirrors the
// sprint/epic handlers' switch shape: ValidationError → 400, FeatureDisabled
// → 404, ErrCollectionNotFound (or "not found" string-match) → 404, else 500.
func writeCollectionError(w http.ResponseWriter, err error) {
	if _, ok := err.(*service.ValidationError); ok {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := err.(*service.FeatureDisabledError); ok {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if errors.Is(err, sqlstore.ErrCollectionNotFound) || errors.Is(err, sqlstore.ErrTaskNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

// ---- collection CRUD --------------------------------------------------------

func (s *Server) listCollections(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "active"
	}
	collections, err := s.svc.Collection.List(status)
	if err != nil {
		writeCollectionError(w, err)
		return
	}
	if collections == nil {
		collections = []sqlstore.CollectionRecord{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"collections": collectionsJSON(collections)})
}

func (s *Server) getCollection(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	collection, err := s.svc.Collection.Get(id)
	if err != nil {
		writeCollectionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, collectionJSON(collection))
}

func (s *Server) createCollection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	collection, err := s.svc.Collection.Create(service.CollectionCreateInput{
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		writeCollectionError(w, err)
		return
	}

	s.sse.Broadcast("collection.created", map[string]interface{}{
		"collection_id": collection.ID,
		"name":          collection.Name,
	})
	writeJSON(w, http.StatusCreated, collectionJSON(collection))
}

func (s *Server) updateCollection(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req map[string]interface{}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	input := service.CollectionUpdateInput{}
	if v, ok := req["name"].(string); ok {
		input.Name = &v
	}
	if v, ok := req["description"].(string); ok {
		input.Description = &v
	}

	if err := s.svc.Collection.Update(id, input); err != nil {
		writeCollectionError(w, err)
		return
	}

	collection, err := s.svc.Collection.Get(id)
	if err != nil {
		writeCollectionError(w, err)
		return
	}

	s.sse.Broadcast("collection.updated", map[string]interface{}{"collection_id": id})
	writeJSON(w, http.StatusOK, collectionJSON(collection))
}

func (s *Server) archiveCollection(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.Collection.Archive(id); err != nil {
		writeCollectionError(w, err)
		return
	}
	s.sse.Broadcast("collection.archived", map[string]interface{}{"collection_id": id})
	collection, err := s.svc.Collection.Get(id)
	if err != nil {
		writeCollectionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, collectionJSON(collection))
}

func (s *Server) unarchiveCollection(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.svc.Collection.Unarchive(id); err != nil {
		writeCollectionError(w, err)
		return
	}
	s.sse.Broadcast("collection.unarchived", map[string]interface{}{"collection_id": id})
	collection, err := s.svc.Collection.Get(id)
	if err != nil {
		writeCollectionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, collectionJSON(collection))
}

// ---- collection ↔ task wiring ----------------------------------------------

func (s *Server) listCollectionTasks(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tasks, err := s.svc.Collection.ListCollectionTasks(id)
	if err != nil {
		writeCollectionError(w, err)
		return
	}
	if tasks == nil {
		tasks = []sqlstore.TaskRecord{}
	}
	out, err := s.tasksJSON(tasks)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tasks": out})
}

func (s *Server) addTaskToCollection(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		TaskID   string `json:"task_id"`
		Position int    `json:"position"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.svc.Collection.AddTask(id, req.TaskID, req.Position); err != nil {
		writeCollectionError(w, err)
		return
	}
	s.sse.Broadcast("collection.task_added", map[string]interface{}{
		"collection_id": id, "task_id": req.TaskID,
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"collection_id": id,
		"task_id":       req.TaskID,
		"added":         true,
	})
}

func (s *Server) removeTaskFromCollection(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	taskID := chi.URLParam(r, "task_id")
	if err := s.svc.Collection.RemoveTask(taskID); err != nil {
		writeCollectionError(w, err)
		return
	}
	s.sse.Broadcast("collection.task_removed", map[string]interface{}{
		"collection_id": id, "task_id": taskID,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reorderCollectionTasks(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		TaskIDs []string `json:"task_ids"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.svc.Collection.ReorderTasks(id, req.TaskIDs); err != nil {
		writeCollectionError(w, err)
		return
	}
	s.sse.Broadcast("collection.tasks_reordered", map[string]interface{}{
		"collection_id": id, "count": len(req.TaskIDs),
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"collection_id": id,
		"reordered":     len(req.TaskIDs),
	})
}

func (s *Server) moveTaskToCollection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID             string `json:"task_id"`
		TargetCollectionID string `json:"target_collection_id"`
		Position           int    `json:"position"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.svc.Collection.MoveTask(req.TaskID, req.TargetCollectionID, req.Position); err != nil {
		writeCollectionError(w, err)
		return
	}
	s.sse.Broadcast("collection.task_moved", map[string]interface{}{
		"task_id": req.TaskID, "target_collection_id": req.TargetCollectionID,
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"task_id":              req.TaskID,
		"target_collection_id": req.TargetCollectionID,
		"moved":                true,
	})
}

// ---- inbox ------------------------------------------------------------------

func (s *Server) listInboxTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.svc.Collection.ListInboxTasks()
	if err != nil {
		writeCollectionError(w, err)
		return
	}
	if tasks == nil {
		tasks = []sqlstore.TaskRecord{}
	}
	out, err := s.tasksJSON(tasks)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tasks": out})
}

func (s *Server) addTaskToInbox(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TaskID string `json:"task_id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := s.svc.Collection.AddToInbox(req.TaskID); err != nil {
		writeCollectionError(w, err)
		return
	}
	s.sse.Broadcast("collection.inbox_added", map[string]interface{}{"task_id": req.TaskID})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"task_id":  req.TaskID,
		"in_inbox": true,
	})
}
