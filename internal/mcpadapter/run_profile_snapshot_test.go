package mcpadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
)

func TestRunProfileSnapshotReadPaths(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	if _, err := store.DB().Exec(`INSERT INTO tasks (id,title,status) VALUES ('snapshot-reads','snapshot reads','done')`); err != nil {
		t.Fatal("create synthetic task row")
	}

	rows := []struct {
		id       int
		snapshot any
		allowed  []string
	}{
		{1, `{"provider":"openai","model":"gpt-current","executor":"cli","runtime_kind":"jsonrpc-stdio","permission_mode":"acceptEdits","launch_profile_id":"worker.current","role":"worker","tier":"standard","api_key":"sk-DUMMY-current"}`, []string{"gpt-current", "worker.current"}},
		{2, `{"Profile":{"ID":"reviewer.legacy","Role":"reviewer","Tier":"trusted","DisplayName":"sk-DUMMY-display"},"AgentProfile":{"Provider":"anthropic","Model":"claude-legacy","Executor":"cli","RuntimeKind":"subprocess-per-turn","PermissionMode":"plan","APIKey":"sk-DUMMY-legacy","SystemPrompt":"sk-DUMMY-prompt","Args":["sk-DUMMY-arg"]},"AgentProfileName":"sk-DUMMY-name","Provenance":"sk-DUMMY-provenance"}`, []string{"claude-legacy", "reviewer.legacy"}},
		{3, `{"api_key":"sk-DUMMY-malformed"`, nil},
		{4, `["sk-DUMMY-array"]`, nil},
		{5, "", nil},
		{6, nil, nil},
		{7, `{"provider":"ollama","api_key":"sk-DUMMY-extra","system_prompt":"sk-DUMMY-extra-prompt","unknown":"sk-DUMMY-unknown"}`, []string{"ollama"}},
	}
	for _, row := range rows {
		if _, err := store.DB().Exec(`INSERT INTO runs (id,task_id,executor,status,started_at,profile_snapshot) VALUES (?, 'snapshot-reads','cli','done','2026-10-10 00:00:00',?)`, row.id, row.snapshot); err != nil {
			t.Fatal("create synthetic run row")
		}
	}

	svc := service.New(store)
	server := httptest.NewServer(httpserver.New(svc, nil))
	defer server.Close()
	adapter := mcpadapter.New(svc, nil)

	for _, row := range rows {
		resp, err := http.Get(server.URL + "/api/v1/runs/" + strconv.Itoa(row.id))
		if err != nil {
			t.Fatal("REST run get failed")
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatal("REST run get returned an unexpected response")
		}
		assertSafeSnapshotOutput(t, body, row.allowed)

		result, err := adapter.Server().CallTool(context.Background(), "torque_run_get", map[string]any{"id": strconv.Itoa(row.id)})
		if err != nil {
			t.Fatal("MCP run get failed")
		}
		body, err = json.Marshal(result)
		if err != nil {
			t.Fatal("marshal MCP run get response")
		}
		assertSafeSnapshotOutput(t, body, row.allowed)
	}

	resp, err := http.Get(server.URL + "/api/v1/runs?task_id=snapshot-reads")
	if err != nil {
		t.Fatal("REST run list failed")
	}
	restList, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatal("REST run list returned an unexpected response")
	}
	assertSafeSnapshotOutput(t, restList, []string{"gpt-current", "worker.current", "claude-legacy", "reviewer.legacy", "ollama"})

	result, err := adapter.Server().CallTool(context.Background(), "torque_run_list", map[string]any{"task_id": "snapshot-reads", "verbose": "true"})
	if err != nil {
		t.Fatal("MCP verbose run list failed")
	}
	mcpList, err := json.Marshal(result)
	if err != nil {
		t.Fatal("marshal MCP verbose run list response")
	}
	assertSafeSnapshotOutput(t, mcpList, []string{"gpt-current", "worker.current", "claude-legacy", "reviewer.legacy", "ollama"})
}

func assertSafeSnapshotOutput(t *testing.T, output []byte, allowed []string) {
	t.Helper()
	if bytes.Contains(output, []byte("sk-DUMMY-")) {
		t.Fatal("secret-shaped value present in output")
	}
	for _, value := range allowed {
		if !bytes.Contains(output, []byte(value)) {
			t.Fatal("allowlisted value missing from output")
		}
	}
}
