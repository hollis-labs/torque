package service

import (
	"context"
	"fmt"
	"strings"
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
//
// entityType is validated against sqlstore.ValidCommentEntityTypes
// (ADR-0004 §5's Comment target inventory: task/project/epic/sprint) — an
// empty value defaults to task first, same as the pre-existing store-layer
// default in AddComment. This is the ENT-COMMENT fix for the "documented as
// future, never shipped" entity_type gap: previously ANY string was
// silently accepted (the comments table has no FK to validate against —
// see migration 019's rationale), so "project"/"epic"/"sprint" already
// round-tripped through the DB, but nothing ever confirmed a caller-supplied
// value was one of the entity kinds Comment actually documents support for.
// Callers that need to bypass this (e.g. the store-layer polymorphism test
// exercising an arbitrary "collection" entity_type) call
// sqlstore.Store.AddComment directly, below this validation.
func (s *CommentService) Add(entityType, entityID, author, content string) (*sqlstore.CommentRecord, error) {
	if entityType == "" {
		entityType = sqlstore.EntityTypeTask
	}
	// Emptiness backstop (CW-20260903-0044). The MCP tools check content
	// before they get here so they can share one error text; this guard
	// covers every OTHER caller of the service — the HTTP POST /comments
	// route, and anything added later. A comment that stores nothing removes
	// exactly the audit trail someone later relies on, so the fence belongs
	// at the layer all writes pass through, not only at the one surface the
	// defect was reported against.
	if strings.TrimSpace(content) == "" {
		return nil, &ValidationError{
			Field:   "content",
			Message: "content is required",
		}
	}
	if !sqlstore.ValidCommentEntityTypes[entityType] {
		return nil, &ValidationError{
			Field:   "entity_type",
			Message: fmt.Sprintf("must be one of: task, project, epic, sprint (got %q)", entityType),
		}
	}
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

// CommentTarget names one (entity_type, entity_id) ref for BulkAdd.
type CommentTarget struct {
	EntityType string
	EntityID   string
}

// BulkAdd posts the same comment text to many entity refs in one call (e.g.
// broadcast a note to every task in a sprint). Unlike Task's BulkUpdate/
// BulkDelete/BulkTag (bulk.go's RunBulk applied to an existing ids[]),
// BulkAdd CREATES a new comment per target rather than operating on
// existing rows — there is no pre-existing "id" to key partial-success
// failures by, so each target's own (entity_type, entity_id) pair is used
// as RunBulk's id string instead (formatted "entity_type:entity_id").
// Successfully created records are returned alongside the RunBulk-shaped
// (succeeded ids, failed) pair so callers get both the created records AND
// the uniform bulk partial-success envelope.
func (s *CommentService) BulkAdd(targets []CommentTarget, author, content string) (created []*sqlstore.CommentRecord, succeededKeys []string, failed []BulkItemError) {
	byKey := make(map[string]CommentTarget, len(targets))
	keys := make([]string, len(targets))
	for i, t := range targets {
		key := fmt.Sprintf("%s:%s", t.EntityType, t.EntityID)
		keys[i] = key
		byKey[key] = t
	}

	succeededKeys, failed = RunBulk(keys, func(key string) error {
		t := byKey[key]
		rec, err := s.Add(t.EntityType, t.EntityID, author, content)
		if err != nil {
			return err
		}
		created = append(created, rec)
		return nil
	})
	return created, succeededKeys, failed
}

// List returns all comments for the given entity, oldest first. Unpaginated
// — the simple/stable primitive the HTTP API and torque_comment_add's
// "review the thread" doc text point to. torque_comment_list (MCP) uses
// ListFiltered/Search's richer filter+sort+cursor path instead.
func (s *CommentService) List(entityType, entityID string) ([]sqlstore.CommentRecord, error) {
	return s.store.ListCommentsForEntity(entityType, entityID)
}

// ListForTask is sugar for List(EntityTypeTask, taskID).
func (s *CommentService) ListForTask(taskID string) ([]sqlstore.CommentRecord, error) {
	return s.List(sqlstore.EntityTypeTask, taskID)
}

// CountForEntity returns how many comments exist on an entity. Used by
// torque_task_get to report thread totals alongside a tail window without
// loading the thread (CW-20260910-0057).
func (s *CommentService) CountForEntity(entityType, entityID string) (int, error) {
	if entityType == "" {
		entityType = sqlstore.EntityTypeTask
	}
	return s.store.CountCommentsForEntity(entityType, entityID)
}

// Search returns comments matching the filter. The caller is responsible for
// enforcing any required-field contract (e.g. non-empty Search) at the
// MCP/HTTP layer — the store accepts an empty Search as "no content filter".
// Also used by torque_comment_list (via ListFiltered) since list and search
// share one underlying query shape at the store layer (ADR-0004 §3: they
// stay distinct TOOLS with distinct defaults, not a distinct query builder).
func (s *CommentService) Search(f sqlstore.CommentFilter) ([]sqlstore.CommentRecord, error) {
	return s.store.SearchComments(f)
}

// ListFiltered is Search's alias for torque_comment_list's call site —
// same underlying query, named separately so the MCP handler reads clearly
// (list vs search) even though both funnel through one store method.
func (s *CommentService) ListFiltered(f sqlstore.CommentFilter) ([]sqlstore.CommentRecord, error) {
	return s.store.SearchComments(f)
}

// Update edits an existing comment's content. Author-scoped: only the
// comment's original author may edit it — a mismatched author returns
// *PermissionError (mapped to error.code=permission at the MCP layer), not
// silently applied or a generic not_found. Returns the updated record.
func (s *CommentService) Update(id int64, author, content string) (*sqlstore.CommentRecord, error) {
	existing, err := s.store.GetComment(id)
	if err != nil {
		return nil, err
	}
	if existing.Author != author {
		return nil, &PermissionError{
			Message: fmt.Sprintf("comment %d is authored by %q, not %q", id, existing.Author, author),
		}
	}
	if err := s.store.UpdateComment(id, content); err != nil {
		return nil, err
	}
	return s.store.GetComment(id)
}

// Delete removes a comment. Author-scoped by default: only the comment's
// original author may delete it. Force deliberately bypasses only that
// author-match guard; it is not an authorization model, since author is caller
// supplied text rather than an authenticated principal.
func (s *CommentService) Delete(id int64, author string, force bool) error {
	existing, err := s.store.GetComment(id)
	if err != nil {
		return err
	}
	if !force && existing.Author != author {
		return &PermissionError{
			Message: fmt.Sprintf("comment %d is authored by %q, not %q", id, existing.Author, author),
		}
	}
	return s.store.DeleteComment(id)
}
