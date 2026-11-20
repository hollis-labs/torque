package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrRequestNotFound = errors.New("request not found")
	ErrRequestConflict = errors.New("request conflict")
	ErrRequestInvalid  = errors.New("invalid request")
)

// RequestRecord is the durable request marker on a manual task. Times are UTC.
// Transport delivery receipts remain owned by Tether, keyed by MailMessageID.
type RequestRecord struct {
	TaskID        string     `json:"task_id"`
	RequesterURN  string     `json:"requester_urn"`
	RecipientURN  string     `json:"recipient_urn"`
	MailMessageID string     `json:"mail_message_id"`
	State         string     `json:"state"`
	SubmittedAt   time.Time  `json:"submitted_at"`
	DueAckAt      time.Time  `json:"due_ack_at"`
	AckAt         *time.Time `json:"ack_at"`
	AlertedAt     *time.Time `json:"alerted_at"`
	ClosedAt      *time.Time `json:"closed_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type RequestRegistration struct {
	RequesterURN  string    `json:"requester_urn"`
	RecipientURN  string    `json:"recipient_urn"`
	MailMessageID string    `json:"mail_message_id"`
	DueAckAt      time.Time `json:"due_ack_at"`
}

type RequestTransition struct {
	State   string `json:"state"`
	Summary string `json:"summary,omitempty"`
	Author  string `json:"author,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

const requestTimeFormat = "2006-01-02T15:04:05.000000000Z"

func scanRequest(row interface{ Scan(...any) error }) (*RequestRecord, error) {
	var raw string
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRequestNotFound
		}
		return nil, err
	}
	var r RequestRecord
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) GetRequest(ctx context.Context, id string) (*RequestRecord, error) {
	return scanRequest(s.ReadDB().QueryRowContext(ctx, `SELECT record FROM task_requests WHERE task_id = ?`, id))
}

// RegisterRequest associates an existing manual task with one transport request.
// Repeating the same identity is a no-op; it never resets deadlines or state.
func (s *Store) RegisterRequest(ctx context.Context, id string, in RequestRegistration) (*RequestRecord, error) {
	if !strings.HasPrefix(in.RequesterURN, "msg://") || !strings.HasPrefix(in.RecipientURN, "msg://") || strings.TrimSpace(in.MailMessageID) == "" {
		return nil, fmt.Errorf("%w: requester_urn, recipient_urn (msg://) and mail_message_id are required", ErrRequestInvalid)
	}
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Rollback()
	old, err := scanRequest(w.tx.QueryRow(`SELECT record FROM task_requests WHERE task_id = ?`, id))
	if err == nil {
		if old.RequesterURN != in.RequesterURN || old.RecipientURN != in.RecipientURN || old.MailMessageID != in.MailMessageID {
			return nil, ErrRequestConflict
		}
		return old, nil
	}
	if !errors.Is(err, ErrRequestNotFound) {
		return nil, err
	}
	var manual bool
	var status string
	if err = w.tx.QueryRow(`SELECT manual,status FROM tasks WHERE id = ?`, id).Scan(&manual, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRequestNotFound
		}
		return nil, err
	}
	if !manual || status == "done" || status == "archived" {
		return nil, fmt.Errorf("%w: register an open manual task", ErrRequestInvalid)
	}
	var associated string
	err = w.tx.QueryRow(`SELECT task_id FROM task_requests WHERE mail_message_id = ?`, in.MailMessageID).Scan(&associated)
	if err == nil {
		return nil, ErrRequestConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	now := time.Now().UTC()
	if in.DueAckAt.IsZero() {
		in.DueAckAt = now.Add(15 * time.Minute)
	}
	r := &RequestRecord{TaskID: id, RequesterURN: in.RequesterURN, RecipientURN: in.RecipientURN, MailMessageID: in.MailMessageID, State: "submitted", SubmittedAt: now, DueAckAt: in.DueAckAt.UTC(), UpdatedAt: now}
	raw, _ := json.Marshal(r)
	if _, err = w.tx.Exec(`INSERT INTO task_requests(task_id,mail_message_id,state,due_ack_at,awaiting_ack,record) VALUES(?,?,?,?,1,?)`, id, r.MailMessageID, r.State, r.DueAckAt.Format(requestTimeFormat), string(raw)); err != nil {
		return nil, err
	}
	// Keep request tasks filterable with the existing task/tag surfaces.
	if _, err = w.tx.Exec(`INSERT INTO tags(slug,name,description,color,created_at,updated_at) VALUES('request','Request','Durable Tether request','blue',?,?) ON CONFLICT(slug) DO NOTHING`, now, now); err != nil {
		return nil, err
	}
	if _, err = w.tx.Exec(`INSERT INTO task_tags(task_id,tag_slug,sort_order,created_at) SELECT ?, 'request', COALESCE(MAX(sort_order),-1)+1, ? FROM task_tags WHERE task_id=? ON CONFLICT(task_id,tag_slug) DO NOTHING`, id, now, id); err != nil {
		return nil, err
	}
	if err = w.requestMetadata(r); err != nil {
		return nil, err
	}
	if err = w.requestEvent("request.submitted", r, "", ""); err != nil {
		return nil, err
	}
	if err = w.Commit(); err != nil {
		return nil, err
	}
	return r, nil
}

// ListRequests returns a bounded, deterministic page. after_task_id is a keyset
// cursor; the watcher can scan more than one page without starving later rows.
func (s *Store) ListRequests(ctx context.Context, awaiting bool, dueBefore time.Time, after string, limit int) ([]RequestRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	q := `SELECT record FROM task_requests WHERE task_id > ?`
	args := []any{after}
	if awaiting {
		q += ` AND awaiting_ack = 1`
	}
	if !dueBefore.IsZero() {
		q += ` AND due_ack_at <= ?`
		args = append(args, dueBefore.UTC().Format(requestTimeFormat))
	}
	q += ` ORDER BY task_id LIMIT ?`
	args = append(args, limit)
	rows, err := s.ReadDB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RequestRecord{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (w *WriteTx) requestEvent(kind string, r *RequestRecord, from, reason string) error {
	reason, truncated := sanitizeReason(reason)
	payload, err := json.Marshal(struct {
		*RequestRecord
		From   string `json:"from,omitempty"`
		Reason string `json:"reason,omitempty"`
	}{r, from, reason})
	if err != nil {
		return err
	}
	_, err = w.appendEvent(eventSpec{Kind: kind, EntityType: "task", EntityID: r.TaskID, Scope: stateScope{TaskID: r.TaskID}, Payload: payload, Truncated: truncated, PayloadSchema: "urn:hollis-labs:events:request_lifecycle@1"})
	return err
}

func (s *Store) TransitionRequest(ctx context.Context, id string, in RequestTransition) (*RequestRecord, error) {
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Rollback()
	r, err := w.transitionRequest(id, in)
	if err != nil {
		return nil, err
	}
	if err = w.Commit(); err != nil {
		return nil, err
	}
	return r, nil
}

func requestClosed(state string) bool {
	return state == "done" || state == "declined" || state == "blocked"
}

func (w *WriteTx) transitionRequest(id string, in RequestTransition) (*RequestRecord, error) {
	switch in.State {
	case "acknowledged", "in_progress", "done", "declined", "blocked", "unacknowledged_alert":
	default:
		return nil, fmt.Errorf("%w: unknown state %q", ErrRequestInvalid, in.State)
	}
	r, err := scanRequest(w.tx.QueryRow(`SELECT record FROM task_requests WHERE task_id = ?`, id))
	if err != nil {
		return nil, err
	}
	// Retries are side-effect-free, including terminal reply retries.
	if r.State == in.State || (in.State == "acknowledged" && r.AckAt != nil) {
		return r, nil
	}
	if in.State == "unacknowledged_alert" && (r.AckAt != nil || r.AlertedAt != nil || requestClosed(r.State)) {
		return r, nil
	}
	if requestClosed(r.State) {
		return nil, fmt.Errorf("%w: request is closed", ErrRequestConflict)
	}
	if requestClosed(in.State) && strings.TrimSpace(in.Summary) == "" {
		return nil, fmt.Errorf("%w: final summary is required", ErrRequestInvalid)
	}
	now := time.Now().UTC()
	r.UpdatedAt = now
	from := r.State
	if in.State == "unacknowledged_alert" {
		r.AlertedAt = &now
	} else {
		if r.AckAt == nil {
			r.AckAt = &now
			r.State = "acknowledged"
			if err = w.requestEvent("request.acknowledged", r, from, ""); err != nil {
				return nil, err
			}
		}
		r.State = in.State
		if requestClosed(in.State) {
			r.ClosedAt = &now
		}
	}
	if err = w.requestMetadata(r); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(r)
	awaiting := 0
	if r.AckAt == nil && r.AlertedAt == nil && !requestClosed(r.State) {
		awaiting = 1
	}
	if _, err = w.tx.Exec(`UPDATE task_requests SET state=?,awaiting_ack=?,record=? WHERE task_id=?`, r.State, awaiting, string(raw), id); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Summary) != "" {
		if err = w.AddComment(&CommentRecord{EntityType: EntityTypeTask, EntityID: id, Author: in.Author, Content: in.Summary}); err != nil {
			return nil, err
		}
	}
	if in.State == "unacknowledged_alert" {
		if err = w.requestEvent("request.unacknowledged_alert", r, from, in.Reason); err != nil {
			return nil, err
		}
	} else {
		if err = w.requestEvent("request.state_changed", r, from, in.Reason); err != nil {
			return nil, err
		}
		status := "doing"
		if in.State == "done" || in.State == "declined" {
			status = "done"
		}
		if in.State == "blocked" {
			status = "blocked"
		}
		if err = w.TransitionTask(id, status); err != nil {
			return nil, err
		}
		if requestClosed(in.State) {
			if err = w.requestEvent("request.closed", r, from, in.Reason); err != nil {
				return nil, err
			}
		}
	}
	return r, nil
}

// Claim from the original persisted comment, never edits or quoted prose.
// The comment author remains audit text; transport authentication is Tether's.
func (w *WriteTx) claimRequestComment(c *CommentRecord) error {
	line := strings.SplitN(strings.TrimSpace(c.Content), "\n", 2)[0]
	if c.EntityType != EntityTypeTask || !(line == "STATUS started" || strings.HasPrefix(line, "STATUS started ") || strings.HasPrefix(line, "STATUS started:")) {
		return nil
	}
	_, err := w.transitionRequest(c.EntityID, RequestTransition{State: "acknowledged"})
	if errors.Is(err, ErrRequestNotFound) || errors.Is(err, ErrRequestConflict) {
		return nil
	}
	return err
}

// Mirror the marker into ordinary task reads without replacing other metadata.
func (w *WriteTx) requestMetadata(r *RequestRecord) error {
	var raw sql.NullString
	if err := w.tx.QueryRow(`SELECT metadata FROM tasks WHERE id=?`, r.TaskID).Scan(&raw); err != nil {
		return err
	}
	meta := map[string]any{}
	if raw.Valid && raw.String != "" {
		if err := json.Unmarshal([]byte(raw.String), &meta); err != nil {
			return err
		}
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta["request"] = r
	body, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	_, err = w.tx.Exec(`UPDATE tasks SET metadata=?,updated_at=? WHERE id=?`, string(body), updatedAtNow(), r.TaskID)
	return err
}
