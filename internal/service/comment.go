package service

import (
	"context"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/writequeue"
)

const commentPersistTimeout = 5 * time.Second

// CommentObserver is invoked synchronously after a comment is persisted.
// Implementations must be non-blocking — observers run on the call path of
// every torque_comment_add. Used by CW-20260509-0028 layer 2 to spot the
// orchestrator's session-complete marker.
type CommentObserver interface {
	ObserveComment(ctx context.Context, c *sqlstore.CommentRecord)
}

// TaskTransitionObserver is invoked synchronously after Task.Transition or
// Task.ForceTransition successfully writes a new status. Implementations
// must be non-blocking. Used by CW-20260509-0028 layer 1 to detect plan-
// terminal transitions (which don't ride the scheduler.EventBus because
// plan FSM moves are agent-driven, not worker-driven).
type TaskTransitionObserver interface {
	ObserveTaskTransition(ctx context.Context, taskID, fromStatus, toStatus string)
}

// PlanPhaseEvent describes a structural change to a plan's phases[]. Kind
// is one of "added" | "removed". For "added", Name and Order carry the
// new phase's metadata; for "removed" they are empty. Used by
// CW-20260519-0126 so durable agents (orchestrator, project manager) can
// react to plan structural changes without polling torque_plan_get.
type PlanPhaseEvent struct {
	Kind      string // "added" | "removed"
	PlanID    string
	PhaseID   string
	PhaseName string // populated for "added"
	Order     int    // populated for "added"
}

// PlanPhaseObserver is invoked synchronously after PlanService.AddPhase or
// PlanService.RemovePhase successfully writes the updated plan metadata.
// Implementations must be non-blocking — the observer fires on the
// request path. Mirrors the TaskTransitionObserver / CommentObserver
// pattern so the daemon can bridge service-layer events onto the
// scheduler.EventBus.
type PlanPhaseObserver interface {
	ObservePlanPhase(ctx context.Context, ev PlanPhaseEvent)
}

// CommentService provides business logic for entity comments.
//
// As of CW-20260503-0003 the comments table is polymorphic — comments are
// keyed by (entity_type, entity_id) rather than tied to a task. The Add and
// List helpers here mirror that shape; AddForTask / ListForTask remain as
// task-specific sugar where the call site only ever operates on tasks.
type CommentService struct {
	store    *sqlstore.Store
	writer   writequeue.TelemetryWriter
	observer CommentObserver // optional; nil disables the observer hook
}

// SetTelemetryWriter installs an optional telemetry sink for persisted
// comments. Nil restores direct-write behavior.
func (s *CommentService) SetTelemetryWriter(w writequeue.TelemetryWriter) {
	s.writer = w
}

func (s *CommentService) telemetryWriter() writequeue.TelemetryWriter {
	if s.writer != nil {
		return s.writer
	}
	return writequeue.NewDirect(s.store)
}

// SetObserver installs a CommentObserver that runs after every successful Add.
// nil clears the observer. Not goroutine-safe with concurrent Add calls;
// install once at bootstrap before serving traffic.
func (s *CommentService) SetObserver(o CommentObserver) {
	s.observer = o
}

// Add inserts a new comment on an arbitrary entity and returns the persisted
// record (including id and created_at populated by the DB).
func (s *CommentService) Add(entityType, entityID, author, content string) (*sqlstore.CommentRecord, error) {
	rec := &sqlstore.CommentRecord{
		EntityType: entityType,
		EntityID:   entityID,
		Author:     author,
		Content:    content,
	}
	ctx, cancel := context.WithTimeout(context.Background(), commentPersistTimeout)
	defer cancel()
	if err := s.telemetryWriter().AddComment(ctx, rec); err != nil {
		return nil, err
	}
	if s.observer != nil {
		s.observer.ObserveComment(context.Background(), rec)
	}
	return rec, nil
}

// AddForTask is sugar for Add(EntityTypeTask, taskID, …).
func (s *CommentService) AddForTask(taskID, author, content string) (*sqlstore.CommentRecord, error) {
	return s.Add(sqlstore.EntityTypeTask, taskID, author, content)
}

// List returns all comments for the given entity, oldest first.
func (s *CommentService) List(entityType, entityID string) ([]sqlstore.CommentRecord, error) {
	return s.store.ListCommentsForEntity(entityType, entityID)
}

// ListForTask is sugar for List(EntityTypeTask, taskID).
func (s *CommentService) ListForTask(taskID string) ([]sqlstore.CommentRecord, error) {
	return s.List(sqlstore.EntityTypeTask, taskID)
}

// Search returns comments matching the filter. The caller is responsible for
// enforcing any required-field contract (e.g. non-empty Search) at the
// MCP/HTTP layer — the store accepts an empty Search as "no content filter".
func (s *CommentService) Search(f sqlstore.CommentFilter) ([]sqlstore.CommentRecord, error) {
	return s.store.SearchComments(f)
}
