package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/writequeue"
	"github.com/hollis-labs/torque/internal/runtime/executor"
)

// writeRunEvent appends a run event to the store. Errors are logged but not
// propagated — run event persistence is best-effort observability, not part
// of the lifecycle's correctness contract. A failed write never fails a task.
//
// runID may be 0 (mapped to NULL) for transitions that pre-date the run record.
func writeRunEvent(ctx context.Context, writer writequeue.TelemetryWriter, runID int64, taskID, eventType string, payload interface{}) {
	if writer == nil {
		return
	}
	var payloadStr string
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			log.Printf("[scheduler] run_event marshal error: %v", err)
			return
		}
		payloadStr = string(b)
	}

	rec := &sqlstore.RunEventRecord{
		TaskID:  taskID,
		Type:    eventType,
		Payload: payloadStr,
	}
	if runID > 0 {
		rec.RunID = sql.NullInt64{Int64: runID, Valid: true}
	}

	if ctx == nil {
		ctx = context.Background()
	}
	if err := writer.AppendRunEvent(ctx, rec); err != nil {
		log.Printf("[scheduler] run_event append failed (task=%s type=%s): %v", taskID, eventType, err)
	}
}

// runEventType maps an ExecutionEvent type to a run_events.type string.
func runEventType(event executor.ExecutionEvent) string {
	switch event.Type {
	case executor.EventLog:
		return "log"
	case executor.EventArtifact:
		return "artifact"
	case executor.EventTokenUsage:
		return "tokens"
	case executor.EventProgress:
		return "progress"
	case executor.EventToolUse:
		return "tool_use"
	default:
		return "unknown"
	}
}

// runEventPayload builds a JSON-serializable payload for a run_event row.
// Returns nil for events with no meaningful payload.
func runEventPayload(event executor.ExecutionEvent) interface{} {
	switch event.Type {
	case executor.EventLog:
		return map[string]string{"line": event.Content}
	case executor.EventArtifact:
		if event.Artifact == nil {
			return nil
		}
		// Build an explicit map so the payload schema is stable regardless of
		// whether the Artifact struct later gains unexported fields or JSON tags.
		return map[string]interface{}{
			"type":      event.Artifact.Type,
			"content":   event.Artifact.Content,
			"url":       event.Artifact.URL,
			"file_path": event.Artifact.FilePath,
		}
	case executor.EventTokenUsage:
		if event.Tokens == nil {
			return nil
		}
		return map[string]interface{}{
			"prompt":     event.Tokens.PromptTokens,
			"completion": event.Tokens.CompletionTokens,
			"cost":       event.Tokens.Cost,
		}
	case executor.EventProgress:
		if event.Progress == nil {
			return nil
		}
		return map[string]float64{"value": *event.Progress}
	case executor.EventToolUse:
		if event.ToolUse == nil {
			return nil
		}
		return map[string]interface{}{
			"tool_name":    event.ToolUse.Name,
			"args_summary": event.ToolUse.ArgsSummary,
		}
	default:
		return nil
	}
}
