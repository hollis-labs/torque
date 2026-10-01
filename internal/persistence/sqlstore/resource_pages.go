package sqlstore

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/torque/internal/service/pagination"
)

// ResourcePageFilter is a normalized bounded query for the remaining list families.
// Internal scheduler/recovery enumeration continues to use the existing methods.
type ResourcePageFilter struct {
	Limit                                                                         int
	Offset                                                                        int
	SortBy, SortDir, AfterSortValue, AfterID                                      string
	Search, Status, TaskID, ProjectID, SessionID, CollectionID, ParentID, PhaseID string
	SprintID, EpicID, Kind, Type, RunID                                           string
	Priority                                                                      *int
	TagSlugs                                                                      []string
	IncludeArchived                                                               bool
}

type ResourceRow struct {
	Record        any
	SortValue, ID string
}

type resourceSpec struct {
	table, columns, id string
	sorts              map[string]string
}

func resourcePageSpec(resource string) (resourceSpec, error) {
	task := resourceSpec{"tasks", taskSelectCols, "id", map[string]string{"priority": "priority", "status": "status", "created_at": "created_at", "updated_at": "updated_at"}}
	switch resource {
	case "plans", "plan_children", "collection_tasks", "collection_inbox":
		if strings.HasPrefix(resource, "collection_") {
			task.sorts["position"] = "COALESCE(collection_position, 9223372036854775807)"
		}
		return task, nil
	case "sessions":
		return resourceSpec{"sessions", sessionSelectCols, "id", map[string]string{"created_at": "created_at", "state": "state"}}, nil
	case "session_checkpoints":
		return resourceSpec{"session_checkpoints", "id, session_id, payload, resume_hint, note, created_at", "id", map[string]string{"created_at": "created_at"}}, nil
	case "artifacts":
		return resourceSpec{"artifacts", artifactSelectCols, "id", map[string]string{"created_at": "created_at", "type": "type"}}, nil
	case "collections":
		return resourceSpec{"collections", "id, name, description, archived_at, created_at, updated_at", "id", map[string]string{"name": "name", "status": "CASE WHEN archived_at IS NULL THEN 'active' ELSE 'archived' END", "created_at": "created_at", "updated_at": "updated_at"}}, nil
	case "templates":
		return resourceSpec{"task_templates", templateSelectCols, "id || ':' || CAST(version AS TEXT)", map[string]string{"name": "name", "kind": "kind", "created_at": "created_at", "updated_at": "updated_at"}}, nil
	case "checkpoints", "pending_checkpoints":
		return resourceSpec{"checkpoints", checkpointSelectCols, "correlation_id", map[string]string{"created_at": "emitted_at", "status": "status"}}, nil
	default:
		return resourceSpec{}, fmt.Errorf("unknown list resource %q", resource)
	}
}

func resourcePredicates(resource string, f ResourcePageFilter) ([]string, []any) {
	var where []string
	var args []any
	eq := func(col, val string) {
		if val != "" {
			where = append(where, col+" = ?")
			args = append(args, val)
		}
	}
	switch resource {
	case "plans", "plan_children", "collection_tasks", "collection_inbox":
		if resource == "plans" {
			eq("kind", "plan")
		}
		if resource == "plan_children" {
			eq("parent_id", f.ParentID)
		}
		if resource == "collection_tasks" {
			eq("collection_id", f.CollectionID)
		}
		if resource == "collection_inbox" {
			where = append(where, "added_to_collections_at IS NOT NULL AND collection_id IS NULL")
		}
		eq("status", f.Status)
		eq("project_id", f.ProjectID)
		eq("sprint_id", f.SprintID)
		eq("epic_id", f.EpicID)
		if f.Priority != nil {
			where = append(where, "priority = ?")
			args = append(args, *f.Priority)
		}
		if len(f.TagSlugs) > 0 {
			for _, slug := range normalizeTagSlugs(f.TagSlugs) {
				where = append(where, "id IN (SELECT task_id FROM task_tags WHERE tag_slug = ?)")
				args = append(args, slug)
			}
		}
		if f.PhaseID != "" {
			where = append(where, "json_extract(metadata, '$.phase_id') = ?")
			args = append(args, f.PhaseID)
		}
		if f.Search != "" {
			where = append(where, "(id LIKE ? OR title LIKE ? OR description LIKE ?)")
			for range 3 {
				args = append(args, "%"+f.Search+"%")
			}
		}
	case "sessions":
		eq("state", f.Status)
		eq("task_id", f.TaskID)
		eq("project_id", f.ProjectID)
		if f.Search != "" {
			where = append(where, "(id LIKE ? OR task_id LIKE ? OR workdir LIKE ?)")
			for range 3 {
				args = append(args, "%"+f.Search+"%")
			}
		}
	case "session_checkpoints":
		eq("session_id", f.SessionID)
		if f.Search != "" {
			where = append(where, "(id LIKE ? OR note LIKE ?)")
			args = append(args, "%"+f.Search+"%", "%"+f.Search+"%")
		}
	case "artifacts":
		eq("task_id", f.TaskID)
		eq("type", f.Type)
		eq("run_id", f.RunID)
		if f.Search != "" {
			where = append(where, "(content LIKE ? OR url LIKE ? OR file_path LIKE ?)")
			for range 3 {
				args = append(args, "%"+f.Search+"%")
			}
		}
	case "collections":
		if f.Status == "active" {
			where = append(where, "archived_at IS NULL")
		}
		if f.Status == "archived" {
			where = append(where, "archived_at IS NOT NULL")
		}
		if f.Search != "" {
			where = append(where, "(id LIKE ? OR name LIKE ? OR description LIKE ?)")
			for range 3 {
				args = append(args, "%"+f.Search+"%")
			}
		}
	case "templates":
		eq("kind", f.Kind)
		if !f.IncludeArchived {
			where = append(where, "is_archived = 0")
		}
		if f.Search != "" {
			where = append(where, "(id LIKE ? OR name LIKE ? OR description LIKE ?)")
			for range 3 {
				args = append(args, "%"+f.Search+"%")
			}
		}
	case "checkpoints", "pending_checkpoints":
		eq("task_id", f.TaskID)
		if f.Search != "" {
			where = append(where, "(correlation_id LIKE ? OR payload_json LIKE ? OR type LIKE ?)")
			for range 3 {
				args = append(args, "%"+f.Search+"%")
			}
		}
		if resource == "pending_checkpoints" {
			eq("status", "pending")
		} else {
			eq("status", f.Status)
		}
	}
	return where, args
}

func (s *Store) CountResourcePage(resource string, f ResourcePageFilter) (int, error) {
	spec, err := resourcePageSpec(resource)
	if err != nil {
		return 0, err
	}
	where, args := resourcePredicates(resource, f)
	if _, pg := s.dialect.(postgresDialect); pg {
		for i := range where {
			where[i] = strings.ReplaceAll(where[i], "json_extract(metadata, '$.phase_id')", "metadata->>'phase_id'")
		}
	}
	q := "SELECT COUNT(*) FROM " + spec.table
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	var count int
	err = s.ReadDB().QueryRow(q, args...).Scan(&count)
	return count, err
}

func (s *Store) ListResourcePage(resource string, f ResourcePageFilter) ([]ResourceRow, error) {
	spec, err := resourcePageSpec(resource)
	if err != nil {
		return nil, err
	}
	col, ok := spec.sorts[f.SortBy]
	if !ok {
		return nil, fmt.Errorf("unsupported sort %q", f.SortBy)
	}
	if f.SortDir != "asc" && f.SortDir != "desc" {
		return nil, fmt.Errorf("invalid sort direction")
	}
	if f.Limit < 1 || f.Limit > pagination.MaxLimit+1 {
		return nil, fmt.Errorf("invalid page bound")
	}
	where, args := resourcePredicates(resource, f)
	if _, pg := s.dialect.(postgresDialect); pg {
		for i := range where {
			where[i] = strings.ReplaceAll(where[i], "json_extract(metadata, '$.phase_id')", "metadata->>'phase_id'")
		}
	}
	orderCol := col
	// emitted_at is the task-checkpoint creation timestamp.
	if col == "emitted_at" {
		orderCol = strings.ReplaceAll(s.timestampSortKey("created_at"), "created_at", "emitted_at")
	} else {
		orderCol = s.timestampSortKey(col)
	}
	if f.AfterID != "" {
		var value any = f.AfterSortValue
		if isTimestampSortColumn(col) || col == "emitted_at" {
			value, err = s.timestampArg(f.AfterSortValue)
		}
		if f.SortBy == "priority" || f.SortBy == "position" {
			value, err = strconv.ParseInt(f.AfterSortValue, 10, 64)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCursor, err)
		}
		var id any = f.AfterID
		if resource == "artifacts" {
			id, err = strconv.ParseInt(f.AfterID, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: invalid artifact id", ErrInvalidCursor)
			}
		}
		op := ">"
		if f.SortDir == "desc" {
			op = "<"
		}
		where = append(where, fmt.Sprintf("(%s %s ? OR (%s = ? AND %s > ?))", orderCol, op, orderCol, spec.id))
		args = append(args, value, value, id)
	}
	q := "SELECT " + spec.columns + " FROM " + spec.table
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY " + orderCol + " " + f.SortDir + ", " + spec.id + " ASC LIMIT ?"
	args = append(args, f.Limit)
	if f.Offset > 0 {
		q += " OFFSET ?"
		args = append(args, f.Offset)
	}
	rows, err := s.ReadDB().Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ResourceRow, 0)
	for rows.Next() {
		rec, err := scanResourceRecord(resource, rows)
		if err != nil {
			return nil, err
		}
		value, id := ResourceCursor(rec, f.SortBy)
		out = append(out, ResourceRow{rec, value, id})
	}
	return out, rows.Err()
}

func scanResourceRecord(resource string, rows *sql.Rows) (any, error) {
	switch resource {
	case "plans", "plan_children", "collection_tasks", "collection_inbox":
		v, e := scanTask(rows)
		if e != nil {
			return nil, e
		}
		return *v, nil
	case "sessions":
		return scanSession(rows)
	case "session_checkpoints":
		v := &SessionCheckpointRecord{}
		e := rows.Scan(&v.ID, &v.SessionID, &v.Payload, &v.ResumeHint, &v.Note, &v.CreatedAt)
		return v, e
	case "templates":
		v, e := scanTemplate(rows)
		if e != nil {
			return nil, e
		}
		return *v, nil
	case "checkpoints", "pending_checkpoints":
		v, e := scanCheckpoint(rows)
		if e != nil {
			return nil, e
		}
		return *v, nil
	case "collections":
		var v CollectionRecord
		e := rows.Scan(&v.ID, &v.Name, &v.Description, &v.ArchivedAt, &v.CreatedAt, &v.UpdatedAt)
		return v, e
	case "artifacts":
		var v ArtifactRecord
		e := rows.Scan(&v.ID, &v.TaskID, &v.RunID, &v.Type, &v.Content, &v.URL, &v.FilePath, &v.Metadata, &v.CreatedAt)
		return v, e
	}
	return nil, fmt.Errorf("unknown resource")
}

// ResourceCursor uses the same non-null scalar keys as the SQL ordering.
func ResourceCursor(record any, by string) (string, string) {
	stamp := func(t time.Time) string { return t.UTC().Format(SQLiteDatetimeLayoutWithFractional) }
	switch v := record.(type) {
	case TaskRecord:
		key := strconv.Itoa(v.Priority)
		switch by {
		case "status":
			key = v.Status
		case "created_at":
			key = stamp(v.CreatedAt)
		case "updated_at":
			key = stamp(v.UpdatedAt)
		case "position":
			key = "9223372036854775807"
			if v.CollectionPosition.Valid {
				key = strconv.FormatInt(v.CollectionPosition.Int64, 10)
			}
		}
		return key, v.ID
	case *SessionRecord:
		if by == "state" {
			return v.State, v.ID
		}
		return stamp(v.CreatedAt), v.ID
	case *SessionCheckpointRecord:
		return stamp(v.CreatedAt), v.ID
	case ArtifactRecord:
		if by == "type" {
			return v.Type, strconv.FormatInt(v.ID, 10)
		}
		return stamp(v.CreatedAt), strconv.FormatInt(v.ID, 10)
	case CheckpointRecord:
		if by == "status" {
			return v.Status, v.CorrelationID
		}
		return stamp(v.EmittedAt), v.CorrelationID
	case CollectionRecord:
		key := v.Name
		switch by {
		case "status":
			key = "active"
			if v.ArchivedAt.Valid {
				key = "archived"
			}
		case "created_at":
			key = stamp(v.CreatedAt)
		case "updated_at":
			key = stamp(v.UpdatedAt)
		}
		return key, v.ID
	case TemplateRecord:
		key := v.Name
		switch by {
		case "kind":
			key = v.Kind
		case "created_at":
			key = stamp(v.CreatedAt)
		case "updated_at":
			key = stamp(v.UpdatedAt)
		}
		return key, v.ID + ":" + strconv.Itoa(v.Version)
	}
	panic("unsupported resource cursor record")
}
