package mcpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// WithSelfReportProxy explicitly makes local stdio use the daemon's writer.
// Empty endpoint/token load the same config as the scheduler status proxy.
// Loopback adapters owned by the daemon continue to call the service directly.
func (a *Adapter) WithSelfReportProxy(endpoint, token string) *Adapter {
	a.selfReportProxy = true
	a.selfReportEndpoint, a.selfReportToken = endpoint, token
	return a
}

// BindReportSession supplies trusted launch context to the loopback before boot.
func (a *Adapter) BindReportSession(id string) { a.reportSession.Store(id) }

func (a *Adapter) registerSelfReportTools() {
	for _, name := range []string{"torque_task_progress", "torque_task_data_emit"} {
		raw, err := catalog.ToolSchema(name + ".input@1")
		if err != nil {
			panic(err)
		}
		var schema map[string]any
		if err = json.Unmarshal(raw, &schema); err != nil {
			panic(err)
		}
		required := []string{}
		for _, v := range schema["required"].([]any) {
			required = append(required, v.(string))
		}
		// Loopback permits implicit task_id, while retaining all other constraints.
		if a.loopbackTaskID != "" {
			filtered := []string{}
			for _, v := range required {
				if v != "task_id" {
					filtered = append(filtered, v)
				}
			}
			required = filtered
			schema["required"] = required
		}
		outRaw, err := catalog.ToolSchema(name + ".output@1")
		if err != nil {
			panic(err)
		}
		var out map[string]any
		if err = json.Unmarshal(outRaw, &out); err != nil {
			panic(err)
		}
		// Torque's MCP transport wraps the catalog result in its standard envelope.
		output := map[string]any{"type": "object", "required": []string{"ok", "data"}, "properties": map[string]any{"ok": map[string]any{"const": true}, "data": out}}
		spec := toolSpec{Name: name, Description: schema["description"].(string) + " Unlike torque_task_checkpoint_emit, never parks. Requires torque.write.", Properties: schema["properties"].(map[string]any), Required: required, InputSchema: schema, OutputSchema: output}
		a.addTool(spec, func(ctx context.Context, in map[string]any) (any, error) { return a.handleSelfReport(ctx, name, in) })
	}
}

func (a *Adapter) handleSelfReport(ctx context.Context, tool string, req map[string]any) (any, error) {
	in := make(map[string]any, len(req))
	for k, v := range req {
		if !strings.HasPrefix(k, "_") {
			in[k] = v
		}
	}
	if a.loopbackTaskID != "" {
		if id, ok := in["task_id"]; ok && id != a.loopbackTaskID {
			return errResult(ErrCodeArgInvalid, "task_id must match the loopback task", "task_id")
		}
		in["task_id"] = a.loopbackTaskID
	}
	var out map[string]any
	var err error
	if a.selfReportProxy {
		out, err = a.proxySelfReport(ctx, tool, in)
	} else {
		if session, ok := a.reportSession.Load().(string); ok {
			ctx = sqlstore.ContextWithReportSession(ctx, session)
		}
		ctx = sqlstore.ContextWithTrigger(ctx, sqlstore.Trigger{Kind: sqlstore.TriggerAgent, Ref: tool})
		if tool == "torque_task_progress" {
			out, err = a.svc.Task.Progress(ctx, in)
		} else {
			out, err = a.svc.Task.EmitData(ctx, in)
		}
	}
	if err != nil {
		return errFromService(err)
	}
	return okResult(out)
}

func (a *Adapter) proxySelfReport(ctx context.Context, tool string, in map[string]any) (map[string]any, error) {
	endpoint, token := a.selfReportEndpoint, a.selfReportToken
	if endpoint == "" {
		cfg, err := config.Load()
		if err != nil {
			return nil, err
		}
		endpoint = fmt.Sprintf("http://127.0.0.1:%d", cfg.HTTPPort)
		token = cfg.APIToken
	}
	taskID, ok := in["task_id"].(string)
	if !ok || taskID == "" {
		return nil, &service.ValidationError{Field: "task_id", Message: "required"}
	}
	route := "progress"
	if tool == "torque_task_data_emit" {
		route = "data"
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, &service.ValidationError{Message: "input must be JSON"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/api/v1/tasks/"+url.PathEscape(taskID)+"/"+route, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, &service.SelfReportError{Code: "unavailable", Message: "self-report requires the Torque daemon; HTTP proxy unavailable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&failure)
		if failure.Code == "" {
			failure.Code = "unavailable"
		}
		if failure.Error == "" {
			failure.Error = "self-report daemon returned " + resp.Status
		}
		return nil, &service.SelfReportError{Code: failure.Code, Message: failure.Error}
	}
	var out map[string]any
	err = json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&out)
	return out, err
}
