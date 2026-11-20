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

// RequestSubmission is immutable transport intent. RecipientOnline and DueAckAt
// are first-submit liveness/deadline snapshots, not part of retry identity.
// TaskID links only when explicitly supplied; a parent/re thread is unrelated.
type RequestSubmission struct {
	RequesterURN    string    `json:"requester_urn"`
	RecipientURN    string    `json:"recipient_urn"`
	SendKey         string    `json:"send_key"`
	Subject         string    `json:"subject"`
	Body            string    `json:"body"`
	Deliverable     string    `json:"deliverable"`
	TaskID          string    `json:"task_id,omitempty"`
	RecipientOnline *bool     `json:"recipient_online,omitempty"`
	DueAckAt        time.Time `json:"due_ack_at,omitempty"`
}

func singleRequestURN(urn string) bool {
	return strings.HasPrefix(urn, "msg://") && !strings.ContainsAny(urn, ",; \t\r\n") && len(urn) > 6
}

// SubmitRequest creates or explicitly links a manual task and its request in
// one serialized transaction. A sender-scoped send key creates at most one
// task even if the caller crashes before receiving the response.
func (s *Store) SubmitRequest(ctx context.Context, in RequestSubmission) (*RequestRecord, error) {
	if !singleRequestURN(in.RequesterURN) || !singleRequestURN(in.RecipientURN) || strings.TrimSpace(in.SendKey) == "" || strings.TrimSpace(in.Subject) == "" {
		return nil, fmt.Errorf("%w: single requester_urn and recipient_urn, send_key and subject are required", ErrRequestInvalid)
	}
	// Canonical JSON of immutable identity, independent of input field order.
	identity, _ := json.Marshal(struct {
		RequesterURN string `json:"requester_urn"`
		RecipientURN string `json:"recipient_urn"`
		SendKey      string `json:"send_key"`
		Subject      string `json:"subject"`
		Body         string `json:"body"`
		Deliverable  string `json:"deliverable"`
		TaskID       string `json:"task_id"`
	}{in.RequesterURN, in.RecipientURN, in.SendKey, in.Subject, in.Body, in.Deliverable, in.TaskID})
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Rollback()
	var raw, storedIdentity string
	err = w.tx.QueryRow(`SELECT record,submission FROM task_requests WHERE requester_urn=? AND send_key=?`, in.RequesterURN, in.SendKey).Scan(&raw, &storedIdentity)
	if err == nil {
		if storedIdentity != string(identity) {
			return nil, fmt.Errorf("%w: send_key payload changed", ErrRequestConflict)
		}
		var r RequestRecord
		if err = json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		return &r, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	id := in.TaskID
	if id == "" {
		id, err = w.NextTaskID()
		if err != nil {
			return nil, err
		}
		description := in.Body
		if in.Deliverable != "" {
			description += "\n\nDeliverable: " + in.Deliverable
		}
		if err = w.CreateTask(&TaskRecord{ID: id, Title: in.Subject, Description: description, Manual: true, Executor: "cli", SourceType: "agent", SourceRef: nullIfEmpty(in.RequesterURN)}); err != nil {
			return nil, err
		}
	} else {
		var manual bool
		var status string
		if err = w.tx.QueryRow(`SELECT manual,status FROM tasks WHERE id=?`, id).Scan(&manual, &status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrRequestNotFound
			}
			return nil, err
		}
		if !manual || status == "done" || status == "archived" {
			return nil, fmt.Errorf("%w: link an open manual task", ErrRequestInvalid)
		}
		var present string
		err = w.tx.QueryRow(`SELECT task_id FROM task_requests WHERE task_id=?`, id).Scan(&present)
		if err == nil {
			return nil, ErrRequestConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	now := time.Now().UTC()
	due := in.DueAckAt.UTC()
	if due.IsZero() {
		due = now.Add(15 * time.Minute)
	}
	if in.RecipientOnline != nil && !*in.RecipientOnline {
		due = now
	}
	r := &RequestRecord{TaskID: id, RecipientOnline: in.RecipientOnline == nil || *in.RecipientOnline, SendKey: in.SendKey, RequesterURN: in.RequesterURN, RecipientURN: in.RecipientURN, State: "submitted", SubmittedAt: now, DueAckAt: due, UpdatedAt: now}
	if err = w.insertRequest(r); err != nil {
		return nil, err
	}
	if _, err = w.tx.Exec(`UPDATE task_requests SET requester_urn=?,send_key=?,submission=? WHERE task_id=?`, in.RequesterURN, in.SendKey, string(identity), id); err != nil {
		return nil, err
	}
	if err = w.Commit(); err != nil {
		return nil, err
	}
	return r, nil
}

// AttachRequestMail records the transport-generated ID after sending. Retrying
// the same attachment is safe, including after acknowledgment or closure.
func (s *Store) AttachRequestMail(ctx context.Context, id, mailID string) (*RequestRecord, error) {
	if strings.TrimSpace(mailID) == "" {
		return nil, fmt.Errorf("%w: mail_message_id is required", ErrRequestInvalid)
	}
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Rollback()
	r, err := scanRequest(w.tx.QueryRow(`SELECT record FROM task_requests WHERE task_id=?`, id))
	if err != nil {
		return nil, err
	}
	if r.MailMessageID == mailID {
		return r, nil
	}
	if r.MailMessageID != "" {
		return nil, ErrRequestConflict
	}
	var associated string
	err = w.tx.QueryRow(`SELECT task_id FROM task_requests WHERE mail_message_id=?`, mailID).Scan(&associated)
	if err == nil {
		return nil, ErrRequestConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	r.MailMessageID = mailID
	r.UpdatedAt = time.Now().UTC()
	raw, _ := json.Marshal(r)
	if _, err = w.tx.Exec(`UPDATE task_requests SET mail_message_id=?,record=? WHERE task_id=?`, mailID, string(raw), id); err != nil {
		return nil, err
	}
	if err = w.requestMetadata(r); err != nil {
		return nil, err
	}
	if err = w.Commit(); err != nil {
		return nil, err
	}
	return r, nil
}
