package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// SelfReportError carries the C4 error code unchanged across HTTP and MCP.
type SelfReportError = sqlstore.SelfReportError

var reportSchemas sync.Map

func validateSelfReport(tool string, input map[string]any) (string, error) {
	cached, ok := reportSchemas.Load(tool)
	if !ok {
		raw, err := catalog.ToolSchema(tool + ".input@1")
		if err != nil {
			return "", err
		}
		var schema jsonschema.Schema
		if err = json.Unmarshal(raw, &schema); err != nil {
			return "", err
		}
		resolved, err := schema.Resolve(nil)
		if err != nil {
			return "", err
		}
		cached, _ = reportSchemas.LoadOrStore(tool, resolved)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return "", &ValidationError{Message: "input must be JSON"}
	}
	// Validate the JSON value, while retaining json.Number in the original
	// input so inline data and its digest never round large integers.
	var value any
	if err = json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	if err = cached.(*jsonschema.Resolved).Validate(value); err != nil {
		return "", &ValidationError{Message: err.Error()}
	}
	for _, field := range []string{"run_id", "artifact_id"} {
		if v, present := input[field]; present {
			if _, err := sqlstore.SelfReportNumericID(v.(string)); err != nil {
				return "", &ValidationError{Field: field, Message: err.Error()}
			}
		}
	}
	if data, ok := input["data"]; ok {
		encoded, err := json.Marshal(data)
		if err != nil {
			return "", err
		}
		if len(encoded) > 16384 {
			return "", &SelfReportError{Code: "data_too_large", Message: "inline data exceeds 16 KiB; create it with torque_artifact_create and pass artifact_id"}
		}
	}
	canonical := make(map[string]any, len(input))
	for k, v := range input {
		if k != "idempotency_key" {
			canonical[k] = v
		}
	}
	raw, err = json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("canonical input: %w", err)
	}
	// Canonicalize number spellings too (1, 1.0 and 1e0 are one input),
	// without rounding data integers through float64. Tagged nodes keep a
	// caller object from colliding with our numeric representation.
	var valueForHash any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&valueForHash); err != nil {
		return "", err
	}
	raw, err = json.Marshal(canonicalReportValue(valueForHash))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// Progress reports without a transition, checkpoint, wait or parking operation.
func (s *TaskService) Progress(ctx context.Context, input map[string]any) (map[string]any, error) {
	return s.selfReport(ctx, "torque_task_progress", input)
}

// EmitData is independent of progress_detail and the progress rate limit.
func (s *TaskService) EmitData(ctx context.Context, input map[string]any) (map[string]any, error) {
	return s.selfReport(ctx, "torque_task_data_emit", input)
}

func (s *TaskService) selfReport(ctx context.Context, tool string, input map[string]any) (map[string]any, error) {
	digest, err := validateSelfReport(tool, input)
	if err != nil {
		return nil, err
	}
	tx, err := s.store.BeginWriteTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	result, err := tx.SelfReport(tool, input, digest)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func canonicalReportValue(value any) any {
	switch v := value.(type) {
	case json.Number:
		n, ok := new(big.Rat).SetString(string(v))
		if !ok {
			return []any{"number", string(v)}
		}
		return []any{"number", n.RatString()}
	case map[string]any:
		object := make(map[string]any, len(v))
		for key, val := range v {
			object[key] = canonicalReportValue(val)
		}
		return []any{"object", object}
	case []any:
		array := make([]any, len(v))
		for i, val := range v {
			array[i] = canonicalReportValue(val)
		}
		return []any{"array", array}
	case string:
		return []any{"string", v}
	case bool:
		return []any{"boolean", v}
	default:
		return []any{"null"}
	}
}
