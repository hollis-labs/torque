package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
			f.intakes = append(f.intakes, a)
			require.Equal(t, "torque_auto", a["source"])
			key := a["intake_key"].(string)
			id, ok := f.items[key]
			if !ok {
				id = "auto-" + string(rune('a'+len(f.items))) + "-item"
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

// newOpsHarness wires OpsSync into DispatchSync. registry may be nil (Ops only).
func newOpsHarness(t *testing.T, svc *service.Service, f *opsFixture, registry *identityFixture) *opsHarness {
	h := &opsHarness{clock: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	o, e := service.NewOpsSync(svc.Store(), service.OpsSyncConfig{URL: f.srv.URL, TokenFile: f.file, PollInterval: time.Hour, Timeout: time.Second})
	require.NoError(t, e)
	o.SetNowForTest(func() time.Time { return h.clock })
	cfg := service.DispatchSyncConfig{PollInterval: time.Hour, Timeout: time.Second}
	if registry != nil {
		cfg.RegistryURL, cfg.RegistryTokenFile = registry.srv.URL, registry.file
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

func TestOpsLinkInheritsFromParentAndPlanWithoutIntake(t *testing.T) {
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
	require.NoError(t, h.ops.Tick(ctx))

	require.Equal(t, "ops-system-of-record", taskMeta(t, svc, parent.ID)["ops_id"])
	for _, id := range []string{child.ID, grandchild.ID} {
		m := taskMeta(t, svc, id)
		require.Equal(t, "ops-system-of-record", m["ops_id"])
		require.Equal(t, "ops-system-of-record", m["workstream_id"])
		require.Equal(t, "parent", m["ops_link"].(map[string]any)["source"])
	}
	require.Empty(t, f.intakes, "inheritance never calls intake")
}

func TestOpsIntakeNeverRefusesAndRetriesUntilDeployed(t *testing.T) {
	svc := setupService(t)
	f := newOpsFixture(t)
	f.set(func() { f.intakeMissing = true })
	h := newOpsHarness(t, svc, f, nil)
	ctx := context.Background()

	task, e := svc.Task.Create(service.TaskCreateInput{Title: "orphan work"})
	require.NoError(t, e, "a missing workstream never refuses the create")
	require.NoError(t, h.ops.Tick(ctx))
	m := taskMeta(t, svc, task.ID)
	require.NotContains(t, m, "ops_id")
	require.Equal(t, "pending", m["ops_link"].(map[string]any)["source"])
	require.Equal(t, "torque:"+task.ID, m["ops_link"].(map[string]any)["from"])

	// Ops v2 ships item_intake; the retry is not due yet, then it is.
	f.set(func() { f.intakeMissing = false })
	require.NoError(t, h.ops.Tick(ctx))
	require.Empty(t, f.intakes)
	h.clock = h.clock.Add(time.Minute)
	require.NoError(t, h.ops.Tick(ctx))
	require.Len(t, f.intakes, 1)
	require.Equal(t, "torque:"+task.ID, f.intakes[0]["intake_key"])
	m = taskMeta(t, svc, task.ID)
	require.Equal(t, "auto-a-item", m["ops_id"])
	require.Equal(t, "intake", m["ops_link"].(map[string]any)["source"])
}

func TestOpsIntakeKeyedOnEpicSharesOneItem(t *testing.T) {
	svc := setupService(t)
	f := newOpsFixture(t)
	h := newOpsHarness(t, svc, f, nil)
	ctx := context.Background()
	svc.Feature.Enable("epics")
	epic, e := svc.Epic.Create(service.EpicCreateInput{Name: "Nanite overnight"})
	require.NoError(t, e)
	a, e := svc.Task.Create(service.TaskCreateInput{Title: "a", EpicID: epic.ID})
	require.NoError(t, e)
	b, e := svc.Task.Create(service.TaskCreateInput{Title: "b", EpicID: epic.ID})
	require.NoError(t, e)
	require.NoError(t, h.ops.Tick(ctx))
	require.Len(t, f.intakes, 2)
	for _, in := range f.intakes {
		require.Equal(t, "torque:"+epic.ID, in["intake_key"])
		require.Equal(t, "Nanite overnight", in["title"])
	}
	require.Equal(t, taskMeta(t, svc, a.ID)["ops_id"], taskMeta(t, svc, b.ID)["ops_id"])
}

func TestOpsDispatchKeysRegistryOnItemAndNeverMintsPerTask(t *testing.T) {
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

	f.set(func() { f.intakeMissing = true })
	orphan, e := svc.Task.Create(service.TaskCreateInput{Title: "orphan"})
	require.NoError(t, e)
	require.NoError(t, h.disp.Prepare(ctx, orphan), "dispatch proceeds while Ops intake is unavailable")
	require.Equal(t, []string{"ops-system-of-record"}, reg.logicalKeys, "no per-task Registry workstream is minted")
}

func TestOpsStatusPostsTaskTransitionsWithItemAndWorkstream(t *testing.T) {
	svc := setupService(t)
	f := newOpsFixture(t)
	reg := newIdentityFixture(t)
	h := newOpsHarness(t, svc, f, reg)
	ctx := context.Background()

	task, e := svc.Task.Create(service.TaskCreateInput{Title: "build g2", Metadata: map[string]any{"ops_id": "ops-system-of-record"}})
	require.NoError(t, e)
	require.NoError(t, h.disp.Prepare(ctx, task))
	for _, s := range []string{"doing", "review", "done"} {
		require.NoError(t, svc.Task.Transition(ctx, task.ID, s))
	}
	f.set(func() { f.statusDown = true })
	require.Error(t, h.disp.Tick(ctx))
	require.Empty(t, f.posted())
	f.set(func() { f.statusDown = false })
	require.NoError(t, h.disp.Tick(ctx))
	require.NoError(t, h.disp.Tick(ctx))

	got := f.posted()
	require.Len(t, got, 3, "one post per transition, no run or unit duplicates")
	kinds := []string{}
	for _, a := range got {
		kinds = append(kinds, a["kind"].(string))
		require.Equal(t, task.ID, a["task"])
		require.Equal(t, "ops-system-of-record", a["ops_id"])
		require.Equal(t, registryID, a["workstream_id"])
		require.Equal(t, "torque://task/"+task.ID, a["ref"])
		require.NotContains(t, a, "namespace", "callers never choose the status namespace")
	}
	require.Equal(t, []string{"task_started", "task_review", "task_done"}, kinds)
	require.Equal(t, []string{"started", "note", "done"}, []string{got[0]["state"].(string), got[1]["state"].(string), got[2]["state"].(string)})
}

func TestOpsStatusLinksHandStartedTaskOnDoing(t *testing.T) {
	svc := setupService(t)
	f := newOpsFixture(t)
	h := newOpsHarness(t, svc, f, nil)
	ctx := context.Background()
	parent, e := svc.Task.Create(service.TaskCreateInput{Title: "plan", Metadata: map[string]any{"ops_id": "nanite-release"}})
	require.NoError(t, e)
	// Created before Ops was enabled would not be scanned; simulate by moving the
	// scan past it, then a hand-started agent takes the child straight to doing.
	child, e := svc.Task.Create(service.TaskCreateInput{Title: "worker task", ParentID: parent.ID})
	require.NoError(t, e)
	require.NoError(t, svc.Task.Transition(ctx, child.ID, "doing"))
	require.NoError(t, h.disp.Tick(ctx))
	got := f.posted()
	require.Len(t, got, 1)
	require.Equal(t, "nanite-release", got[0]["ops_id"])
	require.NotContains(t, got[0], "workstream_id", "no Registry binding without a dispatch")
	require.Equal(t, "nanite-release", taskMeta(t, svc, child.ID)["ops_id"])
}

func TestOpsSyncRequiresCredentialFile(t *testing.T) {
	svc := setupService(t)
	_, e := service.NewOpsSync(svc.Store(), service.OpsSyncConfig{URL: "http://127.0.0.1:8994"})
	require.Error(t, e)
	o, e := service.NewOpsSync(svc.Store(), service.OpsSyncConfig{})
	require.NoError(t, e)
	require.False(t, o.Enabled())
}
