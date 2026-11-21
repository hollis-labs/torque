package sqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrDispatchWorkstreamClosed = errors.New("dispatch workstream binding is closed")

// DispatchWorkstream is a Registry binding and its durable retry marker.
// ProjectID is the local PRJ key; RegistryProject is the authority-scoped
// project:slug domain; NamespaceProject is the sealed status namespace slug.
type DispatchWorkstream struct {
	TaskID, Key, ProjectID, RegistryProject, NamespaceProject, ID, State, LastError string
	Attempts                                                                        int
}

// ReserveDispatchWorkstream preserves the first successful binding for a logical
// key. An explicitly changed plan key starts a new binding; retries do not.
func (s *Store) ReserveDispatchWorkstream(ctx context.Context, taskID, key, project, registryProject, namespace string) (DispatchWorkstream, error) {
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return DispatchWorkstream{}, err
	}
	defer w.Rollback()
	_, err = w.tx.Exec(`UPDATE dispatch_workstreams SET active=0 WHERE task_id=? AND (logical_key<>? OR project_id<>?)`, taskID, key, project)
	if err != nil {
		return DispatchWorkstream{}, err
	}
	_, err = w.tx.Exec(`INSERT INTO dispatch_workstreams(task_id,logical_key,project_id,registry_project,namespace_project) VALUES (?,?,?,?,?)
 ON CONFLICT(task_id,logical_key,project_id) DO UPDATE SET active=1,
 registry_project=CASE WHEN dispatch_workstreams.registry_project='' AND dispatch_workstreams.canonical_id='' THEN excluded.registry_project ELSE dispatch_workstreams.registry_project END,
 namespace_project=CASE WHEN dispatch_workstreams.registry_project='' AND dispatch_workstreams.canonical_id='' THEN excluded.namespace_project ELSE dispatch_workstreams.namespace_project END`, taskID, key, project, registryProject, namespace)
	if err != nil {
		return DispatchWorkstream{}, err
	}
	r, err := scanDispatchWorkstream(w.tx.QueryRow(`SELECT task_id,logical_key,project_id,registry_project,namespace_project,canonical_id,state,attempts,last_error FROM dispatch_workstreams WHERE task_id=? AND active=1`, taskID))
	if err != nil {
		return r, err
	}
	stamp, _ := json.Marshal(map[string]any{"key": r.Key, "id": r.ID, "state": r.State, "last_error": r.LastError})
	if _, err = w.tx.Exec(`UPDATE tasks SET metadata=json_set(CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END,'$.dispatch_workstream',json(?),'$.workstream_id',?) WHERE id=?`, string(stamp), r.ID, r.TaskID); err != nil {
		return r, err
	}
	return r, w.Commit()
}
func scanDispatchWorkstream(row interface{ Scan(...any) error }) (r DispatchWorkstream, err error) {
	err = row.Scan(&r.TaskID, &r.Key, &r.ProjectID, &r.RegistryProject, &r.NamespaceProject, &r.ID, &r.State, &r.Attempts, &r.LastError)
	return
}
func (s *Store) DispatchWorkstream(ctx context.Context, taskID string) (DispatchWorkstream, error) {
	return scanDispatchWorkstream(s.ReadDB().QueryRowContext(ctx, `SELECT task_id,logical_key,project_id,registry_project,namespace_project,canonical_id,state,attempts,last_error FROM dispatch_workstreams WHERE task_id=? AND active=1`, taskID))
}
func (s *Store) PendingDispatchWorkstreams(ctx context.Context) ([]DispatchWorkstream, error) {
	rows, err := s.ReadDB().QueryContext(ctx, `SELECT task_id,logical_key,project_id,registry_project,namespace_project,canonical_id,state,attempts,last_error FROM dispatch_workstreams WHERE state='pending' ORDER BY attempts,task_id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DispatchWorkstream
	for rows.Next() {
		r, e := scanDispatchWorkstream(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveDispatchWorkstream atomically stamps the task and any already-created
// executor runs. A late response for an old plan cannot replace a new binding.
func (s *Store) SaveDispatchWorkstream(ctx context.Context, r DispatchWorkstream) error {
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer w.Rollback()
	res, err := w.tx.Exec(`UPDATE dispatch_workstreams SET canonical_id=CASE WHEN canonical_id<>'' THEN canonical_id ELSE ? END,state=?,attempts=attempts+1,last_error=?,registry_project=?,namespace_project=? WHERE task_id=? AND logical_key=? AND project_id=? AND (state<>'closed' OR ?='closed')`, r.ID, r.State, r.LastError, r.RegistryProject, r.NamespaceProject, r.TaskID, r.Key, r.ProjectID, r.State)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var state string
		if err := w.tx.QueryRow(`SELECT state FROM dispatch_workstreams WHERE task_id=? AND logical_key=? AND project_id=?`, r.TaskID, r.Key, r.ProjectID).Scan(&state); err == nil && state == "closed" {
			return ErrDispatchWorkstreamClosed
		}
		return fmt.Errorf("dispatch workstream binding changed")
	}
	// A stale outage response must preserve a canonical ID learned meanwhile.
	if err = w.tx.QueryRow(`SELECT canonical_id FROM dispatch_workstreams WHERE task_id=? AND logical_key=? AND project_id=?`, r.TaskID, r.Key, r.ProjectID).Scan(&r.ID); err != nil {
		return err
	}
	stamp, _ := json.Marshal(map[string]any{"key": r.Key, "id": r.ID, "state": r.State, "last_error": r.LastError})
	_, err = w.tx.Exec(`UPDATE tasks SET metadata=json_set(CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END,'$.dispatch_workstream',json(?),'$.workstream_id',?) WHERE id=? AND EXISTS(SELECT 1 FROM dispatch_workstreams WHERE task_id=? AND logical_key=? AND project_id=? AND active=1)`, string(stamp), r.ID, r.TaskID, r.TaskID, r.Key, r.ProjectID)
	if err != nil {
		return err
	}
	if r.ID != "" {
		if _, err = w.tx.Exec(`UPDATE tasks SET metadata=json_set(metadata,'$.unit_run.workstream_id',?) WHERE id=? AND json_extract(metadata,'$.unit_run') IS NOT NULL AND EXISTS(SELECT 1 FROM dispatch_workstreams WHERE task_id=? AND logical_key=? AND project_id=? AND active=1)`, r.ID, r.TaskID, r.TaskID, r.Key, r.ProjectID); err != nil {
			return err
		}

		if _, err = w.tx.Exec(`UPDATE runs SET workstream_id=? WHERE task_id=? AND (workstream_id IS NULL OR workstream_id='') AND id IN (SELECT e.run_id FROM event_outbox e JOIN dispatch_event_workstreams b ON b.event_id=e.event_id WHERE b.task_id=? AND b.logical_key=? AND b.project_id=?)`, r.ID, r.TaskID, r.TaskID, r.Key, r.ProjectID); err != nil {
			return err
		}
	}
	return w.Commit()
}

// recordUnitLifecycle runs with the unit_run update so lifecycle telemetry
// survives process loss. Re-saving the same state produces no second event.
func (w *WriteTx) recordUnitLifecycle(taskID, previous, current string) error {
	var before, after struct {
		State          string `json:"state"`
		RunID          string `json:"run_id"`
		Key            string `json:"key"`
		WorkstreamID   string `json:"workstream_id"`
		ResultObserved bool   `json:"result_observed"`
		ResultState    string `json:"result_state"`
		Dissolved      bool   `json:"dissolved"`
	}
	_ = json.Unmarshal([]byte(previous), &before)
	if err := json.Unmarshal([]byte(current), &after); err != nil {
		return err
	}
	var states []string
	if before.Key != after.Key || before.State != after.State {
		switch after.State {
		case "forming":
			states = append(states, "dispatched")
		case "active":
			states = append(states, "formed")
		case "failed":
			states = append(states, "failed")
		}
	}
	if after.ResultObserved && !before.ResultObserved {
		states = append(states, "RESULT")
	}
	if after.Dissolved && !before.Dissolved {
		states = append(states, "dissolved")
	}
	for _, state := range states {
		payload, _ := json.Marshal(map[string]string{"state": state, "run_id": after.RunID, "workstream_id": after.WorkstreamID, "dispatch_key": after.Key, "result_state": after.ResultState})
		scope := w.scopeForTask(taskID)
		if after.WorkstreamID != "" {
			scope.WorkstreamID = after.WorkstreamID
		}
		if _, err := w.appendEvent(eventSpec{Kind: "unit.lifecycle", Category: "state", EntityType: "task", EntityID: taskID, Payload: payload, PayloadSchema: "torque.unit.lifecycle@1", Scope: scope}); err != nil {
			return err
		}
	}
	return nil
}

// DispatchEventWorkstream resolves the logical identity sealed with an event.
func (s *Store) DispatchEventWorkstream(ctx context.Context, eventID string) (DispatchWorkstream, error) {
	return scanDispatchWorkstream(s.ReadDB().QueryRowContext(ctx, `SELECT w.task_id,w.logical_key,w.project_id,w.registry_project,w.namespace_project,w.canonical_id,w.state,w.attempts,w.last_error FROM dispatch_event_workstreams e JOIN dispatch_workstreams w ON w.task_id=e.task_id AND w.logical_key=e.logical_key AND w.project_id=e.project_id WHERE e.event_id=?`, eventID))
}
func (s *Store) DispatchWorkstreamForKey(ctx context.Context, r DispatchWorkstream) (DispatchWorkstream, error) {
	return scanDispatchWorkstream(s.ReadDB().QueryRowContext(ctx, `SELECT task_id,logical_key,project_id,registry_project,namespace_project,canonical_id,state,attempts,last_error FROM dispatch_workstreams WHERE task_id=? AND logical_key=? AND project_id=?`, r.TaskID, r.Key, r.ProjectID))
}

// SealDispatchStatus returns the first delivery body for an event, repairing only
// the empty session_id in legacy dispatcher payloads rejected by Tesseract.
func (s *Store) SealDispatchStatus(ctx context.Context, eventID string, payload []byte) ([]byte, error) {
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	defer w.Rollback()
	if _, err = w.tx.Exec(`INSERT OR IGNORE INTO dispatch_status_payloads(event_id,payload) VALUES (?,?)`, eventID, string(payload)); err != nil {
		return nil, err
	}
	var sealed string
	if err = w.tx.QueryRow(`SELECT payload FROM dispatch_status_payloads WHERE event_id=?`, eventID).Scan(&sealed); err != nil {
		return nil, err
	}
	// Older dispatcher payloads were sealed with an empty session_id and were
	// rejected by Tesseract. Repair only that invalid field, retaining the
	// original identity and all other sealed values across retries/restarts.
	var body map[string]json.RawMessage
	if err = json.Unmarshal([]byte(sealed), &body); err != nil {
		return nil, err
	}
	var session string
	if err = json.Unmarshal(body["session_id"], &session); err != nil && len(body["session_id"]) != 0 {
		return nil, err
	}
	if session == "" {
		var data struct {
			RunID  string `json:"run_id"`
			TaskID string `json:"task_id"`
		}
		if err = json.Unmarshal(body["data"], &data); err != nil {
			return nil, err
		}
		identity := data.RunID
		if identity == "" {
			identity = data.TaskID
		}
		if identity == "" {
			identity = eventID
		}
		body["session_id"], err = json.Marshal("torque-dispatcher:" + identity)
		if err != nil {
			return nil, err
		}
		raw, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return nil, marshalErr
		}
		sealed = string(raw)
		if _, err = w.tx.Exec(`UPDATE dispatch_status_payloads SET payload=? WHERE event_id=?`, sealed, eventID); err != nil {
			return nil, err
		}
	}
	if err = w.Commit(); err != nil {
		return nil, err
	}
	return []byte(sealed), nil
}

// AssignDefaultTaskProject fills only an unassigned project; concurrent explicit
// assignments win. The caller validates that the configured project exists.
func (s *Store) AssignDefaultTaskProject(ctx context.Context, taskID, projectID string) error {
	w, err := s.BeginWriteTx(ctx)
	if err != nil {
		return err
	}
	defer w.Rollback()
	if _, err = w.tx.Exec(`UPDATE tasks SET project_id=? WHERE id=? AND COALESCE(project_id,'')=''`, projectID, taskID); err != nil {
		return err
	}
	return w.Commit()
}
