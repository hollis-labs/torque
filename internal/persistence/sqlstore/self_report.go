package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"
)

type SelfReportError struct{ Code, Message string }

func (e *SelfReportError) Error() string { return e.Message }

func SelfReportNumericID(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("expected a positive decimal id")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("expected a positive decimal id")
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("expected a positive decimal id")
	}
	return n, nil
}

func reportString(in map[string]any, key string) string { s, _ := in[key].(string); return s }

// SelfReport accepts service-validated C4 input. All ownership checks, receipt
// lookup, detail resolution, rate reservation and outbox writes share this tx.
func (w *WriteTx) SelfReport(tool string, in map[string]any, digest string) (map[string]any, error) {
	taskID := reportString(in, "task_id")
	scope := stateScope{TaskID: taskID}
	var project, epic, sprint, detail, origin sql.NullString
	err := w.tx.QueryRow(`SELECT project_id,epic_id,sprint_id,progress_detail,progress_detail_source FROM tasks WHERE id=?`, taskID).Scan(&project, &epic, &sprint, &detail, &origin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	scope.ProjectID, scope.EpicID, scope.SprintID = project.String, epic.String, sprint.String
	var launch sql.NullString
	run := reportString(in, "run_id")
	var boundDetail sql.NullString
	if sessionID, _ := w.ctx.Value(reportSessionKey{}).(string); sessionID != "" {
		var owner sql.NullString
		var boundRun sql.NullInt64
		var tether, launchID, workstream sql.NullString
		err := w.tx.QueryRow(`SELECT task_id,run_id,progress_detail,tether_session_id,launch_id,workstream_id FROM sessions WHERE id=?`, sessionID).Scan(&owner, &boundRun, &boundDetail, &tether, &launchID, &workstream)
		if err != nil {
			return nil, err
		}
		// Orchestrator loopbacks can act on other tasks; their own launch must
		// never label a report about one of those other tasks.
		if owner.String == taskID {
			if run == "" && boundRun.Valid {
				run = strconv.FormatInt(boundRun.Int64, 10)
			}
			if run == "" || (boundRun.Valid && run == strconv.FormatInt(boundRun.Int64, 10)) {
				scope.TetherSessionID, scope.LaunchID, scope.WorkstreamID = tether.String, launchID.String, workstream.String
			} else {
				boundDetail = sql.NullString{}
			}
		} else {
			boundDetail = sql.NullString{}
		}
	}
	if run != "" {
		id, err := SelfReportNumericID(run)
		if err != nil {
			return nil, err
		}
		var owner string
		var sessionID, launchID, workstreamID sql.NullString
		err = w.tx.QueryRow(`SELECT task_id,progress_detail,COALESCE(attempt,0),tether_session_id,launch_id,workstream_id FROM runs WHERE id=?`, id).Scan(&owner, &launch, &scope.Attempt, &sessionID, &launchID, &workstreamID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && owner != taskID) {
			return nil, &SelfReportError{Code: "arg_invalid", Message: "run_id must belong to task_id"}
		}
		if err != nil {
			return nil, err
		}
		scope.RunID = sql.NullInt64{Int64: id, Valid: true}
		scope.TetherSessionID, scope.LaunchID, scope.WorkstreamID = sessionID.String, launchID.String, workstreamID.String
	}
	if artifact := reportString(in, "artifact_id"); artifact != "" {
		if err := w.reportArtifactOwner(taskID, artifact); err != nil {
			return nil, err
		}
	}
	if ref := reportString(in, "data_ref"); ref != "" {
		if _, err := SelfReportNumericID(ref); err == nil {
			if err = w.reportArtifactOwner(taskID, ref); err != nil {
				return nil, err
			}
		} else {
			var exists int
			err = w.tx.QueryRow(`SELECT 1 FROM event_outbox WHERE event_id=? AND task_id=? AND kind=?`, ref, taskID, catalog.KindDataEmitted).Scan(&exists)
			if errors.Is(err, sql.ErrNoRows) {
				return nil, &SelfReportError{Code: "arg_invalid", Message: "data_ref must identify an artifact or data event on this task"}
			}
			if err != nil {
				return nil, err
			}
		}
	}
	if boundDetail.Valid && catalog.ProgressDetail(boundDetail.String).Valid() {
		launch = boundDetail
	}
	key := reportString(in, "idempotency_key")
	if key != "" {
		var oldTool, oldDigest, response string
		err = w.tx.QueryRow(`SELECT tool_kind,request_hash,response_json FROM task_self_report_receipts WHERE task_id=? AND idempotency_key=?`, taskID, key).Scan(&oldTool, &oldDigest, &response)
		if err == nil {
			if oldTool != tool || oldDigest != digest {
				return nil, &SelfReportError{Code: "idempotency_conflict", Message: "idempotency_key already identifies a different tool or input"}
			}
			var result map[string]any
			if err := json.Unmarshal([]byte(response), &result); err != nil {
				return nil, err
			}
			if tool == "torque_task_progress" {
				result["emitted"] = false
				result["suppressed_reason"] = "duplicate"
			} else {
				result["duplicate"] = true
			}
			return result, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	result := map[string]any{}
	payload := map[string]any{}
	kind := catalog.KindDataEmitted
	now := time.Now().UTC()
	level := reportString(in, "level")
	if level == "" {
		level = "step"
	}
	if tool == "torque_task_progress" {
		task, template := catalog.ProgressDetail(detail.String), catalog.ProgressDetail("")
		if origin.String == "template" {
			task, template = "", task
		}
		effective, source := catalog.EffectiveProgressDetail(catalog.ProgressDetail(launch.String), task, template)
		result["emitted"], result["effective_level"], result["detail_source"] = false, string(effective), source
		if !effective.Allows(catalog.ProgressDetail(level)) {
			result["suppressed_reason"] = "below_detail"
			return result, nil
		}
		var previous string
		err = w.tx.QueryRow(`SELECT last_emitted_at FROM task_progress_rate WHERE task_id=? AND detail_level=?`, taskID, level).Scan(&previous)
		if err == nil {
			last, err := time.Parse(time.RFC3339Nano, previous)
			if err != nil {
				return nil, fmt.Errorf("progress rate timestamp: %w", err)
			}
			if now.Sub(last) < 2*time.Second {
				result["suppressed_reason"] = "rate_limited"
				return result, nil
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		for _, k := range []string{"label", "summary", "pct", "step", "total_steps", "data_ref"} {
			if v, ok := in[k]; ok {
				payload[k] = v
			}
		}
		payload["detail_level"], payload["effective_level"], payload["detail_source"] = level, string(effective), source
		kind = catalog.KindProgressCheckpoint
	} else {
		payload["schema"] = in["schema"]
		if data, ok := in["data"]; ok {
			payload["data"] = data
		} else {
			artifactID, _ := SelfReportNumericID(reportString(in, "artifact_id"))
			payload["ref"] = "msg://artifact/torque/" + strconv.FormatInt(artifactID, 10)
		}
		if label, ok := in["label"]; ok {
			payload["label"] = label
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	entry, ok := catalog.Lookup(kind)
	if !ok {
		return nil, fmt.Errorf("unregistered self-report kind %q", kind)
	}
	eventID, err := w.appendEvent(eventSpec{Kind: kind, Category: entry.Category, EntityType: "task", EntityID: taskID, Payload: raw, Trigger: ResolveTrigger(w.ctx), Scope: scope, IdempotencyKey: key})
	if err != nil {
		return nil, err
	}
	result["event_id"] = eventID
	if tool == "torque_task_progress" {
		result["emitted"] = true
		_, err = w.tx.Exec(`INSERT INTO task_progress_rate(task_id,detail_level,last_emitted_at) VALUES (?,?,?) ON CONFLICT(task_id,detail_level) DO UPDATE SET last_emitted_at=excluded.last_emitted_at`, taskID, level, now.Format(time.RFC3339Nano))
		if err != nil {
			return nil, err
		}
	} else {
		result["data_ref"] = eventID
	}
	if key != "" {
		response, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		_, err = w.tx.Exec(`INSERT INTO task_self_report_receipts(task_id,idempotency_key,tool_kind,request_hash,event_id,data_ref,response_json) VALUES (?,?,?,?,?,?,?)`, taskID, key, tool, digest, eventID, nullIfEmpty(reportString(result, "data_ref")), string(response))
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (w *WriteTx) reportArtifactOwner(taskID, artifact string) error {
	id, err := SelfReportNumericID(artifact)
	if err != nil {
		return err
	}
	var owner sql.NullString
	err = w.tx.QueryRow(`SELECT task_id FROM artifacts WHERE id=?`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && owner.String != taskID) {
		return &SelfReportError{Code: "arg_invalid", Message: "artifact_id must belong to task_id"}
	}
	return err
}
