package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/require"
)

// opsFixture is a fake Ops service MCP endpoint (stateless JSON-RPC on /mcp).
type opsFixture struct {
	mu            sync.Mutex
	intakeMissing bool // item_intake not deployed yet: JSON-RPC error
	statusDown    bool
	intakes       []map[string]any
	statuses      map[string]map[string]any // idempotency_key -> args
	statusOrder   []string
	items         map[string]string // intake_key -> item id
	srv           *httptest.Server
	file          string
}

func newOpsFixture(t *testing.T) *opsFixture {
	f := &opsFixture{statuses: map[string]map[string]any{}, items: map[string]string{}, file: filepath.Join(t.TempDir(), "ops-operator")}
	require.NoError(t, os.WriteFile(f.file, []byte("ops-operator-token\n"), 0600))
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		require.Equal(t, "/mcp", r.URL.Path)
		require.Equal(t, "Bearer ops-operator-token", r.Header.Get("Authorization"))
		require.NotEmpty(t, r.Header.Get("X-Actor"))
		var req struct {
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "tools/call", req.Method)
		reply := func(v any) {
			raw, _ := json.Marshal(v)
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []any{map[string]any{"type": "text", "text": string(raw)}}, "structuredContent": v}})
		}
		switch req.Params.Name {
		case "item_intake":
			if f.intakeMissing {
				_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32602, "message": "unknown tool"}})
				return
			}
			a := req.Params.Arguments
			allowed := map[string]bool{"id": true, "intake_key": true, "origin": true, "owner": true, "parent": true, "priority": true, "project": true, "purpose": true, "source": true, "stage": true, "title": true}
			for k := range a {
				require.True(t, allowed[k], "item_intake schema refuses %q (additionalProperties false)", k)
			}
			f.intakes = append(f.intakes, a)
			require.Equal(t, "torque_auto", a["source"])
			key := a["intake_key"].(string)
			id, ok := f.items[key]
			if !ok {
				id, _ = a["id"].(string)
				f.items[key] = id
			}
			reply(map[string]any{"id": id, "created": !ok})
		case "status_post":
			if f.statusDown {
				w.WriteHeader(503)
				return
			}
			a := req.Params.Arguments
			require.Equal(t, r.Header.Get("X-Task-Id"), a["task"])
			key := a["idempotency_key"].(string)
			if old, ok := f.statuses[key]; ok {
				require.Equal(t, old, a, "a retry must resend the sealed arguments")
			} else {
				f.statuses[key] = a
				f.statusOrder = append(f.statusOrder, key)
			}
			reply(map[string]any{"write_outcome": "created"})
		default:
			t.Fatalf("unexpected tool %s", req.Params.Name)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}
func (f *opsFixture) set(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }
func (f *opsFixture) posted() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, k := range f.statusOrder {
		out = append(out, f.statuses[k])
	}
	return out
}

type opsHarness struct {
	ops   *service.OpsSync
	disp  *service.DispatchSync
	clock time.Time
}

// newOpsHarness wires OpsSync into DispatchSync. reg (Registry + Tesseract) may be nil.
func newOpsHarness(t *testing.T, svc *service.Service, f *opsFixture, reg *identityFixture) *opsHarness {
	h := &opsHarness{clock: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	o, e := service.NewOpsSync(svc.Store(), service.OpsSyncConfig{URL: f.srv.URL, TokenFile: f.file, PollInterval: time.Hour, Timeout: time.Second})
	require.NoError(t, e)
	o.SetNowForTest(func() time.Time { return h.clock })
	cfg := service.DispatchSyncConfig{PollInterval: time.Hour, Timeout: time.Second}
	if reg != nil {
		cfg = reg.config()
	}
	d, e := service.NewDispatchSync(svc.Store(), cfg)
	require.NoError(t, e)
	d.SetOps(o)
	require.NoError(t, o.Start(context.Background()))
	require.NoError(t, d.Start(context.Background()))
	t.Cleanup(d.Stop)
	t.Cleanup(o.Stop)
	h.ops, h.disp = o, d
	return h
}

func TestOpsCreateOnlyInheritsNeverFilesUnfiled(t *testing.T) {
	svc := setupService(t)
	f := newOpsFixture(t)
	h := newOpsHarness(t, svc, f, nil)
	ctx := context.Background()

	parent, e := svc.Task.Create(service.TaskCreateInput{Title: "parent", Metadata: map[string]any{"workstream_id": "ops-system-of-record"}})
	require.NoError(t, e)
	child, e := svc.Task.Create(service.TaskCreateInput{Title: "child", ParentID: parent.ID})
	require.NoError(t, e)
	grandchild, e := svc.Task.Create(service.TaskCreateInput{Title: "grandchild", ParentID: child.ID})
	require.NoError(t, e)
	orphan, e := svc.Task.Create(service.TaskCreateInput{Title: "orphan"})
	require.NoError(t, e, "a missing workstream never refuses the create")
	require.NoError(t, h.ops.Tick(ctx))

	require.Equal(t, "ops-system-of-record", taskMeta(t, svc, parent.ID)["ops_id"])
	for _, id := range []string{child.ID, grandchild.ID} {
		m := taskMeta(t, svc, id)
		require.Equal(t, "ops-system-of-record", m["ops_id"])
		require.Equal(t, "parent", m["ops_link"].(map[string]any)["source"])
	}
	rec, _ := svc.Task.Get(orphan.ID)
	require.False(t, rec.Metadata.Valid && strings.Contains(rec.Metadata.String, "ops_id"), "no unfiled assignment at create")
	require.Empty(t, f.intakes)
}

func TestOpsAliasesAndEpicInheritance(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")
	f := newOpsFixture(t)
	h := newOpsHarness(t, svc, f, nil)
	ctx := context.Background()
	unit, e := svc.Task.Create(service.TaskCreateInput{Title: "unit", Metadata: map[string]any{"workstream_key": "registry-adoption"}})
	require.NoError(t, e)
	_, e = h.ops.Link(ctx, unit.ID, true)
	require.NoError(t, e)
	require.Equal(t, "registry-adoption", taskMeta(t, svc, unit.ID)["ops_id"])

	epic, e := svc.Epic.Create(service.EpicCreateInput{Name: "Nanite overnight"})
	require.NoError(t, e)
	filed, e := svc.Task.Create(service.TaskCreateInput{Title: "filed", EpicID: epic.ID, Metadata: map[string]any{"ops_id": "nanite-release"}})
	require.NoError(t, e)
	_, e = h.ops.Link(ctx, filed.ID, false)
	require.NoError(t, e)
	sibling, e := svc.Task.Create(service.TaskCreateInput{Title: "sibling", EpicID: epic.ID})
	require.NoError(t, e)
	res, e := h.ops.Link(ctx, sibling.ID, true)
	require.NoError(t, e)
	require.Equal(t, "epic", res.Source)
	require.Equal(t, "nanite-release", taskMeta(t, svc, sibling.ID)["ops_id"])
	require.Empty(t, f.intakes)
}

func TestOpsFirstDoingFilesUnfiledAndIntakeRetries(t *testing.T) {
	svc := setupService(t)
	f := newOpsFixture(t)
	f.set(func() { f.intakeMissing = true })
	reg := newIdentityFixture(t)
	h := newOpsHarness(t, svc, f, reg)
	ctx := context.Background()

	a, e := svc.Task.Create(service.TaskCreateInput{Title: "hand-started a"})
	require.NoError(t, e)
	b, e := svc.Task.Create(service.TaskCreateInput{Title: "hand-started b"})
	require.NoError(t, e)
	for _, id := range []string{a.ID, b.ID} {
		require.NoError(t, svc.Task.Transition(ctx, id, "doing"))
	}
	require.NoError(t, h.disp.Tick(ctx), "status flows while Ops intake is unavailable")
	for _, id := range []string{a.ID, b.ID} {
		m := taskMeta(t, svc, id)
		require.Equal(t, "unfiled-agent-os", m["ops_id"])
		require.Equal(t, true, m["unfiled"])
		require.Equal(t, registryID, m["workstream_id"])
	}
	require.Equal(t, []string{"unfiled-agent-os", "unfiled-agent-os"}, reg.logicalKeys, "Registry is keyed on the item, never the task")
	for _, ev := range reg.events() {
		cs := ev["consumer_state"].(map[string]any)
		require.Equal(t, "unfiled-agent-os", cs["ops_id"])
		require.Equal(t, "task_started", cs["kind"])
	}

	require.NoError(t, h.ops.Tick(ctx))
	require.Empty(t, f.intakes)
	f.set(func() { f.intakeMissing = false })
	h.clock = h.clock.Add(time.Minute)
	require.NoError(t, h.ops.Tick(ctx))
	require.NoError(t, h.ops.Tick(ctx))
	require.Len(t, f.intakes, 1, "one intake per project, done once")
	require.Equal(t, "torque:unfiled:", f.intakes[0]["intake_key"])
	require.Equal(t, "unfiled-agent-os", f.intakes[0]["id"])
	require.NotContains(t, f.intakes[0], "stage")
}

func TestOpsDispatchKeysRegistryOnItem(t *testing.T) {
	svc := setupService(t)
	f := newOpsFixture(t)
	reg := newIdentityFixture(t)
	h := newOpsHarness(t, svc, f, reg)
	ctx := context.Background()

	linked, e := svc.Task.Create(service.TaskCreateInput{Title: "linked", Metadata: map[string]any{"workstream_id": "ops-system-of-record"}})
	require.NoError(t, e)
	require.NoError(t, h.disp.Prepare(ctx, linked))
	require.Equal(t, []string{"ops-system-of-record"}, reg.logicalKeys)
	m := taskMeta(t, svc, linked.ID)
	require.Equal(t, registryID, m["workstream_id"], "the canonical Registry id replaces the slug")
	require.Equal(t, "ops-system-of-record", m["ops_id"], "the item id survives the canonical stamp")

	require.NoError(t, svc.Task.Transition(ctx, linked.ID, "doing"))
	require.NoError(t, svc.Task.Transition(ctx, linked.ID, "review"))
	require.NoError(t, h.disp.Tick(ctx))
	kinds := map[string]bool{}
	for _, ev := range reg.events() {
		cs := ev["consumer_state"].(map[string]any)
		require.Equal(t, "ops-system-of-record", cs["ops_id"])
		require.Equal(t, "ops-system-of-record", ev["data"].(map[string]any)["ops_id"])
		require.Equal(t, registryID, ev["workstream_id"])
		kinds[cs["kind"].(string)] = true
	}
	require.Equal(t, map[string]bool{"task_started": true, "task_review": true}, kinds)
}

func TestOpsSyncRequiresCredentialFile(t *testing.T) {
	svc := setupService(t)
	_, e := service.NewOpsSync(svc.Store(), service.OpsSyncConfig{URL: "http://127.0.0.1:8994"})
	require.Error(t, e)
	o, e := service.NewOpsSync(svc.Store(), service.OpsSyncConfig{})
	require.NoError(t, e)
	require.False(t, o.Enabled())
}

func TestOpsLegacyBoundTaskAndProjectSlug(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")
	f := newOpsFixture(t)
	reg := newIdentityFixture(t)
	h := newOpsHarness(t, svc, f, reg)
	ctx := context.Background()

	// Bound before ops_id existed: only the Registry key names the item.
	legacy, e := svc.Task.Create(service.TaskCreateInput{Title: "legacy", Metadata: map[string]any{"workstream_id": "ops-system-of-record"}})
	require.NoError(t, e)
	_, e = svc.Store().ReadDB().Exec(`UPDATE tasks SET metadata=json_set(metadata,'$.workstream_id',?,'$.dispatch_workstream',json('{"key":"ops-system-of-record","id":"`+registryID+`","state":"ensured"}')) WHERE id=?`, registryID, legacy.ID)
	require.NoError(t, e)
	rec, _ := svc.Task.Get(legacy.ID)
	require.Equal(t, "ops-system-of-record", service.OpsIDOf(rec))

	p, e := svc.Project.Create(service.ProjectCreateInput{Name: "Substrate", RepoPath: t.TempDir()})
	require.NoError(t, e)
	task, e := svc.Task.Create(service.TaskCreateInput{Title: "orphan", ProjectID: p.ID})
	require.NoError(t, e)
	_, e = h.ops.Link(ctx, task.ID, true)
	require.NoError(t, e)
	require.Equal(t, "unfiled-substrate", taskMeta(t, svc, task.ID)["ops_id"])
}
