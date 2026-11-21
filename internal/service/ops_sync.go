package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// Ops system of record (ops/v2 contract rev 3, caster://ops/design.system-of-record-v2).
//
// G1 (CW-20261121-0146): metadata.ops_id (the bare Ops item slug) is the key
// field. A task without one is linked when it first moves to doing or is
// dispatched: inherit from the parent chain (plans included), then from its
// epic, else assign the project's unfiled item unfiled-<project slug> and set
// metadata.unfiled=true. The unfiled item is created once through Ops
// item_intake (intake_key torque:unfiled:<project>); its id is deterministic, so
// linking never waits on Ops. Tasks are never refused, and Torque never mints a
// per-task workstream: the Registry ensure key is the ops_id. At create only
// inheritance runs (no unfiled assignment).
//
// metadata.ops_link records how the id was found (source, from).

// OpsSyncConfig holds the Ops service endpoint and a credential FILE path.
type OpsSyncConfig struct {
	URL, TokenFile string
	// Actor is the stated identity sent as X-Actor on operator-token calls.
	Actor        string
	Timeout      time.Duration
	PollInterval time.Duration
}

func OpsSyncConfigFromEnv() OpsSyncConfig {
	return OpsSyncConfig{
		URL:       os.Getenv("TORQUE_OPS_URL"),
		TokenFile: os.Getenv("TORQUE_OPS_TOKEN_FILE"),
		Actor:     os.Getenv("TORQUE_OPS_ACTOR"),
	}
}

// OpsSync links tasks to Ops items and talks to the Ops service MCP endpoint.
type OpsSync struct {
	store  *sqlstore.Store
	cfg    OpsSyncConfig
	http   *http.Client
	tickMu sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
	now    func() time.Time
}

// opsSlug is the ops/v2 item id grammar (design 2.2).
var opsSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)

// torqueTaskID matches Torque task ids; a dispatch key equal to a task id is the
// legacy per-task fallback, never an Ops item.
var torqueTaskID = regexp.MustCompile(`^[A-Z]+-\d{8}-\d{4}$`)

const opsScanConsumer = "ops-intake-scan-v1"

var errOpsUnavailable = errors.New("ops service unavailable")

func NewOpsSync(store *sqlstore.Store, cfg OpsSyncConfig) (*OpsSync, error) {
	if cfg.URL == "" {
		return nil, nil
	}
	u, e := url.Parse(cfg.URL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("ops integration: invalid endpoint")
	}
	if cfg.TokenFile == "" {
		return nil, fmt.Errorf("ops integration: TORQUE_OPS_URL requires TORQUE_OPS_TOKEN_FILE")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 3 * time.Second
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.Actor == "" {
		cfg.Actor = "msg://service/torque/dispatcher"
	}
	return &OpsSync{
		store: store, cfg: cfg, now: time.Now,
		http: &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

// Enabled reports whether Ops delivery is configured. Safe on nil.
func (o *OpsSync) Enabled() bool { return o != nil && o.cfg.URL != "" }

// Start begins the background loop. The new-task scan starts at today's head on
// first enable: older tasks are linked when they next move (transition or
// dispatch), not backfilled in bulk.
func (o *OpsSync) Start(ctx context.Context) error {
	if !o.Enabled() {
		return nil
	}
	w, e := o.store.BeginWriteTx(ctx)
	if e != nil {
		return e
	}
	defer w.Rollback()
	if _, e = w.Exec(`INSERT OR IGNORE INTO event_outbox_cursors(consumer,last_seq,updated_at) SELECT ?,COALESCE((SELECT MAX(rowid) FROM tasks),0),?`, opsScanConsumer, o.now().UTC().Format(time.RFC3339Nano)); e != nil {
		return e
	}
	if e = w.Commit(); e != nil {
		return e
	}
	ctx, o.cancel = context.WithCancel(ctx)
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		t := time.NewTicker(o.cfg.PollInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if e := o.Tick(ctx); e != nil && ctx.Err() == nil {
					log.Printf("[ops-sync] %v", e)
				}
			}
		}
	}()
	return nil
}

func (o *OpsSync) Stop() {
	if o == nil {
		return
	}
	if o.cancel != nil {
		o.cancel()
	}
	o.wg.Wait()
}

// Tick runs create-time inheritance for new tasks (every create path, including
// requests and internal reviewers) and retries due unfiled-item intakes.
func (o *OpsSync) Tick(ctx context.Context) error {
	if !o.Enabled() {
		return nil
	}
	o.tickMu.Lock()
	defer o.tickMu.Unlock()
	after, e := o.store.OutboxCursorLoad(ctx, opsScanConsumer)
	if e != nil {
		return e
	}
	rows, e := o.store.ReadDB().QueryContext(ctx, `SELECT rowid,id FROM tasks WHERE rowid>? ORDER BY rowid LIMIT 200`, after)
	if e != nil {
		return e
	}
	type row struct {
		rowid int64
		id    string
	}
	var fresh []row
	for rows.Next() {
		var r row
		if e = rows.Scan(&r.rowid, &r.id); e != nil {
			rows.Close()
			return e
		}
		fresh = append(fresh, r)
	}
	rows.Close()
	var errs []error
	for _, r := range fresh {
		if _, e := o.Link(ctx, r.id, false); e != nil {
			errs = append(errs, fmt.Errorf("task %s: %w", r.id, e))
		}
		if e := o.saveScanCursor(ctx, r.rowid); e != nil {
			return e
		}
	}
	if e := o.retryDue(ctx); e != nil {
		errs = append(errs, e)
	}
	return errors.Join(errs...)
}

func (o *OpsSync) saveScanCursor(ctx context.Context, rowid int64) error {
	w, e := o.store.BeginWriteTx(ctx)
	if e != nil {
		return e
	}
	defer w.Rollback()
	if _, e = w.Exec(`INSERT INTO event_outbox_cursors(consumer,last_seq,updated_at) VALUES (?,?,?) ON CONFLICT(consumer) DO UPDATE SET last_seq=MAX(last_seq,excluded.last_seq),updated_at=excluded.updated_at`, opsScanConsumer, rowid, o.now().UTC().Format(time.RFC3339Nano)); e != nil {
		return e
	}
	return w.Commit()
}

// OpsIDOf returns the Ops item id a task already carries, without I/O:
// metadata.ops_id, then the legacy aliases unit_plan.workstream_id and
// workstream_key, then a non-canonical metadata.workstream_id (what ops
// item_link writes). A Torque task id is never an item id.
func OpsIDOf(t *sqlstore.TaskRecord) string {
	m := taskMetadata(t)
	str := func(v any) string { s, _ := v.(string); return strings.TrimSpace(s) }
	ok := func(s string) bool { return opsSlug.MatchString(s) && !torqueTaskID.MatchString(s) }
	if s := str(m["ops_id"]); ok(s) {
		return s
	}
	if p, isMap := m["unit_plan"].(map[string]any); isMap {
		if s := str(p["workstream_id"]); ok(s) {
			return s
		}
	}
	for _, k := range []string{"workstream_key", "workstream_id"} {
		if s := str(m[k]); ok(s) {
			return s
		}
	}
	return ""
}

// OpsLinkResult says how a task got its item.
type OpsLinkResult struct {
	OpsID  string
	Source string // self | parent | plan | epic | unfiled
	From   string // the task, epic or intake key it came from
}

// Link makes sure a task carries metadata.ops_id. atWork=false (create time)
// only inherits; atWork=true (first doing, dispatch, a status event) falls back
// to the project's unfiled item. It never refuses and never mints per task.
func (o *OpsSync) Link(ctx context.Context, taskID string, atWork bool) (OpsLinkResult, error) {
	if !o.Enabled() {
		return OpsLinkResult{}, nil
	}
	t, e := o.store.GetTask(taskID)
	if e != nil {
		if errors.Is(e, sqlstore.ErrTaskNotFound) {
			return OpsLinkResult{}, nil
		}
		return OpsLinkResult{}, e
	}
	m := taskMetadata(t)
	if id := OpsIDOf(t); id != "" {
		res := OpsLinkResult{OpsID: id, Source: "self", From: t.ID}
		if s, _ := m["ops_id"].(string); s != id {
			return res, o.stamp(ctx, t.ID, res, false)
		}
		return res, nil
	}
	// Parent chain; plans are the parents of their phase tasks.
	seen := map[string]bool{t.ID: true}
	cur := t
	for depth := 0; depth < 12 && cur.ParentID.Valid && cur.ParentID.String != "" && !seen[cur.ParentID.String]; depth++ {
		seen[cur.ParentID.String] = true
		p, e := o.store.GetTask(cur.ParentID.String)
		if e != nil {
			break
		}
		if id := OpsIDOf(p); id != "" && !strings.HasPrefix(id, "unfiled-") {
			src := "parent"
			if p.Kind == "plan" {
				src = "plan"
			}
			res := OpsLinkResult{OpsID: id, Source: src, From: p.ID}
			return res, o.stamp(ctx, t.ID, res, false)
		}
		cur = p
	}
	// Epic: epics carry no metadata, so the epic's item is the one its tasks were
	// filed under (most recently updated, unfiled ones excluded).
	if t.EpicID.Valid && t.EpicID.String != "" {
		var id string
		err := o.store.ReadDB().QueryRowContext(ctx, `SELECT json_extract(metadata,'$.ops_id') FROM tasks
 WHERE epic_id=? AND id<>? AND json_valid(metadata) AND COALESCE(json_extract(metadata,'$.ops_id'),'')<>''
 AND json_extract(metadata,'$.ops_id') NOT LIKE 'unfiled-%' ORDER BY updated_at DESC LIMIT 1`, t.EpicID.String, t.ID).Scan(&id)
		if err == nil && opsSlug.MatchString(id) {
			res := OpsLinkResult{OpsID: id, Source: "epic", From: t.EpicID.String}
			return res, o.stamp(ctx, t.ID, res, false)
		}
	}
	if !atWork {
		return OpsLinkResult{}, nil
	}
	project := t.ProjectID.String
	if project == "" {
		project = os.Getenv("TORQUE_UNIT_DEFAULT_PROJECT")
	}
	slug := o.projectSlug(project)
	res := OpsLinkResult{OpsID: truncate("unfiled-"+slug, 64), Source: "unfiled", From: "torque:unfiled:" + project}
	if e := o.enqueueUnfiled(ctx, project, slug, res.OpsID, t.ID); e != nil {
		return res, e
	}
	return res, o.stamp(ctx, t.ID, res, true)
}

// projectSlug resolves a local PRJ id to its namespace slug (Registry
// project:<slug>, else a slug-valid project name, else agent-os).
func (o *OpsSync) projectSlug(localID string) string {
	if localID == "" {
		return "agent-os"
	}
	p, err := o.store.GetProject(localID)
	if err != nil {
		return "agent-os"
	}
	if p.ExternalAuthority == "registry" && strings.HasPrefix(p.ExternalID, "project:") {
		if s := strings.TrimPrefix(p.ExternalID, "project:"); projectSlug.MatchString(s) {
			return s
		}
	}
	if projectSlug.MatchString(p.Name) {
		return p.Name
	}
	return "agent-os"
}

func (o *OpsSync) stamp(ctx context.Context, taskID string, r OpsLinkResult, unfiled bool) error {
	link, _ := json.Marshal(map[string]any{"source": r.Source, "from": r.From, "at": o.now().UTC().Format(time.RFC3339)})
	w, e := o.store.BeginWriteTx(ctx)
	if e != nil {
		return e
	}
	defer w.Rollback()
	unfiledSQL := `json_remove(%s,'$.unfiled')`
	if unfiled {
		unfiledSQL = `json_set(%s,'$.unfiled',json('true'))`
	}
	// workstream_id keeps a canonical Registry id; otherwise it mirrors the slug,
	// which is what ops item_link writes.
	base := `json_set(CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END,
 '$.ops_id',?,'$.ops_link',json(?),
 '$.workstream_id',CASE WHEN json_valid(metadata) AND json_extract(metadata,'$.workstream_id') LIKE 'workstream:%' THEN json_extract(metadata,'$.workstream_id') ELSE ? END)`
	if _, e = w.Exec(`UPDATE tasks SET metadata=`+fmt.Sprintf(unfiledSQL, base)+` WHERE id=?`, r.OpsID, string(link), r.OpsID, taskID); e != nil {
		return e
	}
	return w.Commit()
}

// enqueueUnfiled records (once per project) that the unfiled item must exist.
func (o *OpsSync) enqueueUnfiled(ctx context.Context, project, slug, itemID, origin string) error {
	w, e := o.store.BeginWriteTx(ctx)
	if e != nil {
		return e
	}
	defer w.Rollback()
	if _, e = w.Exec(`INSERT OR IGNORE INTO ops_intake_pending(intake_key,item_id,title,project_id,origin_task,next_at) VALUES (?,?,?,?,?,?)`,
		"torque:unfiled:"+project, itemID, "Unfiled work: "+slug, project, origin, o.now().UTC().Format(time.RFC3339Nano)); e != nil {
		return e
	}
	return w.Commit()
}

func (o *OpsSync) retryDue(ctx context.Context) error {
	rows, e := o.store.ReadDB().QueryContext(ctx, `SELECT intake_key,item_id,title,project_id,origin_task,attempts FROM ops_intake_pending WHERE done_at='' AND next_at<=? ORDER BY attempts,next_at LIMIT 20`, o.now().UTC().Format(time.RFC3339Nano))
	if e != nil {
		return e
	}
	type intake struct {
		key, id, title, project, origin string
		attempts                        int
	}
	var due []intake
	for rows.Next() {
		var in intake
		if e = rows.Scan(&in.key, &in.id, &in.title, &in.project, &in.origin, &in.attempts); e != nil {
			rows.Close()
			return e
		}
		due = append(due, in)
	}
	rows.Close()
	var errs []error
	for _, in := range due {
		args := map[string]any{
			"intake_key": in.key, "id": in.id, "title": in.title, "stage": "in_flight",
			"source": "torque_auto", "origin": map[string]any{"kind": "torque.task", "id": in.origin},
			"idempotency_key": "torque-intake-" + in.key,
		}
		if in.project != "" {
			args["project"] = in.project
		}
		var out struct {
			ID   string `json:"id"`
			Item struct {
				ID string `json:"id"`
			} `json:"item"`
		}
		callErr := o.call(ctx, "item_intake", in.origin, args, &out)
		got := out.ID
		if got == "" {
			got = out.Item.ID
		}
		now := o.now().UTC()
		w, we := o.store.BeginWriteTx(ctx)
		if we != nil {
			return we
		}
		if callErr == nil {
			if got != "" && got != in.id {
				log.Printf("[ops-sync] intake %s returned %s, expected %s", in.key, got, in.id)
			}
			_, we = w.Exec(`UPDATE ops_intake_pending SET done_at=?,last_error='' WHERE intake_key=?`, now.Format(time.RFC3339Nano), in.key)
		} else {
			n := in.attempts + 1
			backoff := time.Duration(1<<min(n, 7)) * 5 * time.Second // 10s .. ~10m
			_, we = w.Exec(`UPDATE ops_intake_pending SET attempts=?,next_at=?,last_error=? WHERE intake_key=?`, n, now.Add(backoff).Format(time.RFC3339Nano), truncate(callErr.Error(), 300), in.key)
			if !errors.Is(callErr, errOpsUnavailable) {
				errs = append(errs, fmt.Errorf("intake %s: %w", in.key, callErr))
			}
		}
		if we == nil {
			we = w.Commit()
		} else {
			w.Rollback()
		}
		if we != nil {
			return we
		}
	}
	return errors.Join(errs...)
}

// opsToolError is a tool-level refusal (isError) from the Ops service.
type opsToolError struct{ Text string }

func (e *opsToolError) Error() string { return "ops tool error: " + e.Text }

// call invokes one Ops MCP tool (stateless JSON-RPC over POST /mcp) with the
// operator token read from file on each call, so rotation needs no restart.
func (o *OpsSync) call(ctx context.Context, tool, taskID string, args map[string]any, result any) error {
	tok, e := os.ReadFile(o.cfg.TokenFile)
	if e != nil {
		return fmt.Errorf("%w: credential unreadable", errOpsUnavailable)
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.cfg.URL, "/")+"/mcp", bytes.NewReader(body))
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(tok)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("X-Actor", o.cfg.Actor)
	if taskID != "" {
		req.Header.Set("X-Task-Id", taskID)
	}
	resp, e := o.http.Do(req)
	if e != nil {
		return fmt.Errorf("%w: %v", errOpsUnavailable, e)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: http %d", errOpsUnavailable, resp.StatusCode)
	}
	var rpc struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			Structured json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	if e = json.Unmarshal(raw, &rpc); e != nil {
		return fmt.Errorf("%w: invalid response", errOpsUnavailable)
	}
	if rpc.Error != nil {
		// Unknown tool (item_intake before the Ops v2 release) is an outage for us: retry later.
		return fmt.Errorf("%w: rpc %d %s", errOpsUnavailable, rpc.Error.Code, truncate(rpc.Error.Message, 200))
	}
	if rpc.Result.IsError {
		text := ""
		if len(rpc.Result.Content) > 0 {
			text = rpc.Result.Content[0].Text
		}
		return &opsToolError{Text: truncate(text, 300)}
	}
	if result == nil {
		return nil
	}
	if len(rpc.Result.Structured) > 0 && string(rpc.Result.Structured) != "null" {
		return json.Unmarshal(rpc.Result.Structured, result)
	}
	if len(rpc.Result.Content) > 0 {
		return json.Unmarshal([]byte(rpc.Result.Content[0].Text), result)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
