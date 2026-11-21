package service

import (
	"bytes"
	"context"
	"database/sql"
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

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// Ops system of record (ops/v2 contract, caster://ops/design.system-of-record-v2).
//
// G1 (CW-20261121-0146): every task carries an Ops item id. A task without one
// inherits its parent's (walking up through plans), and failing that Torque asks
// the Ops service to create or return an item with item_intake, keyed
// torque:<epic id or task id>. The create is never refused: an intake that cannot
// complete is parked in ops_intake_pending and retried.
//
// G2 (CW-20261121-0147): task status transitions are delivered to the Ops
// service's status_post (see DispatchSync.deliverOps) carrying the item id, the
// canonical Registry workstream and the task.
//
// The item id is stamped as metadata.ops_id (authoritative) and, when the task
// has no canonical Registry id yet, as metadata.workstream_id, which is where Ops
// linking writes it. metadata.ops_link records where it came from.

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

// Tick links newly created tasks (every create path, including requests and
// internal reviewers) and retries due intakes.
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
// metadata.ops_id, then workstream_key, then a non-canonical metadata.workstream_id
// (what ops item_link writes). unit_plan.workstream_id is a correlation label in
// ops/v2 (design 2.2 correlation.labels), not an item id, so it is not used here.
func OpsIDOf(t *sqlstore.TaskRecord) string {
	m := taskMetadata(t)
	str := func(v any) string { s, _ := v.(string); return strings.TrimSpace(s) }
	for _, k := range []string{"ops_id", "workstream_key", "workstream_id"} {
		if s := str(m[k]); opsSlug.MatchString(s) && !torqueTaskID.MatchString(s) {
			return s
		}
	}
	return ""
}

// OpsLinkResult says how a task got (or is waiting for) its item.
type OpsLinkResult struct {
	OpsID  string
	Source string // self | parent | plan | intake | pending
	From   string // the task or intake key it came from
}

// Link makes sure a task carries an Ops item id. withIntake=true tries the intake
// call inline (dispatch path); otherwise a needed intake is queued and attempted
// by the background loop right away. It never refuses: on failure the task is
// marked pending and the error is only returned for logging.
func (o *OpsSync) Link(ctx context.Context, taskID string, withIntake bool) (OpsLinkResult, error) {
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
	if id := OpsIDOf(t); id != "" {
		res := OpsLinkResult{OpsID: id, Source: "self", From: t.ID}
		m := taskMetadata(t)
		if s, _ := m["ops_id"].(string); s != id {
			return res, o.stamp(ctx, t.ID, res)
		}
		return res, nil
	}
	// Inherit: parent chain (plans are parents of their phase tasks).
	seen := map[string]bool{t.ID: true}
	cur := t
	for depth := 0; depth < 12 && cur.ParentID.Valid && cur.ParentID.String != "" && !seen[cur.ParentID.String]; depth++ {
		seen[cur.ParentID.String] = true
		p, e := o.store.GetTask(cur.ParentID.String)
		if e != nil {
			break
		}
		if id := OpsIDOf(p); id != "" {
			src := "parent"
			if p.Kind == "plan" {
				src = "plan"
			}
			res := OpsLinkResult{OpsID: id, Source: src, From: p.ID}
			return res, o.stamp(ctx, t.ID, res)
		}
		cur = p
	}
	// No item: intake keyed on the epic (so an epic's tasks share one item) or the task.
	key, title := "torque:"+t.ID, t.Title
	if t.EpicID.Valid && t.EpicID.String != "" {
		key = "torque:" + t.EpicID.String
		if ep, e := o.store.GetEpic(t.EpicID.String); e == nil && ep.Name != "" {
			title = ep.Name
		}
	}
	if e := o.enqueue(ctx, t, key, title); e != nil {
		return OpsLinkResult{}, e
	}
	if !withIntake {
		return OpsLinkResult{Source: "pending", From: key}, nil
	}
	return o.attempt(ctx, t.ID)
}

func (o *OpsSync) stamp(ctx context.Context, taskID string, r OpsLinkResult) error {
	link, _ := json.Marshal(map[string]any{"source": r.Source, "from": r.From, "at": o.now().UTC().Format(time.RFC3339)})
	w, e := o.store.BeginWriteTx(ctx)
	if e != nil {
		return e
	}
	defer w.Rollback()
	// workstream_id gets the slug only while it is not a canonical Registry id:
	// the Registry binding stays authoritative for that key once minted.
	if _, e = w.Exec(`UPDATE tasks SET metadata=json_set(CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END,
 '$.ops_id',?,'$.ops_link',json(?),
 '$.workstream_id',CASE WHEN json_valid(metadata) AND json_extract(metadata,'$.workstream_id') LIKE 'workstream:%' THEN json_extract(metadata,'$.workstream_id') ELSE ? END)
 WHERE id=?`, r.OpsID, string(link), r.OpsID, taskID); e != nil {
		return e
	}
	if _, e = w.Exec(`DELETE FROM ops_intake_pending WHERE task_id=?`, taskID); e != nil {
		return e
	}
	return w.Commit()
}

func (o *OpsSync) enqueue(ctx context.Context, t *sqlstore.TaskRecord, key, title string) error {
	now := o.now().UTC().Format(time.RFC3339Nano)
	w, e := o.store.BeginWriteTx(ctx)
	if e != nil {
		return e
	}
	defer w.Rollback()
	if _, e = w.Exec(`INSERT INTO ops_intake_pending(task_id,intake_key,title,project_id,next_at,created_at) VALUES (?,?,?,?,?,?)
 ON CONFLICT(task_id) DO UPDATE SET intake_key=excluded.intake_key,title=excluded.title,project_id=excluded.project_id`,
		t.ID, key, title, t.ProjectID.String, now, now); e != nil {
		return e
	}
	link, _ := json.Marshal(map[string]any{"source": "pending", "from": key, "at": now})
	if _, e = w.Exec(`UPDATE tasks SET metadata=json_set(CASE WHEN json_valid(metadata) THEN metadata ELSE '{}' END,'$.ops_link',json(?)) WHERE id=?`, string(link), t.ID); e != nil {
		return e
	}
	return w.Commit()
}

func (o *OpsSync) retryDue(ctx context.Context) error {
	rows, e := o.store.ReadDB().QueryContext(ctx, `SELECT task_id FROM ops_intake_pending WHERE next_at<=? ORDER BY attempts,next_at LIMIT 50`, o.now().UTC().Format(time.RFC3339Nano))
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	rows.Close()
	var errs []error
	for _, id := range ids {
		if _, e := o.attempt(ctx, id); e != nil && !errors.Is(e, errOpsUnavailable) {
			errs = append(errs, fmt.Errorf("intake %s: %w", id, e))
		}
	}
	return errors.Join(errs...)
}

// attempt runs one intake for a pending task. Inheritance is re-checked first: a
// parent or an Ops link may have supplied an id in the meantime.
func (o *OpsSync) attempt(ctx context.Context, taskID string) (OpsLinkResult, error) {
	var key, title, project string
	var attempts int
	e := o.store.ReadDB().QueryRowContext(ctx, `SELECT intake_key,title,project_id,attempts FROM ops_intake_pending WHERE task_id=?`, taskID).Scan(&key, &title, &project, &attempts)
	if errors.Is(e, sql.ErrNoRows) {
		return OpsLinkResult{}, nil
	}
	if e != nil {
		return OpsLinkResult{}, e
	}
	if t, ge := o.store.GetTask(taskID); ge == nil {
		if id := OpsIDOf(t); id != "" {
			res := OpsLinkResult{OpsID: id, Source: "self", From: taskID}
			return res, o.stamp(ctx, taskID, res)
		}
	}
	args := map[string]any{
		"intake_key":      key,
		"title":           truncate(title, 200),
		"source":          "torque_auto",
		"origin":          map[string]any{"kind": "torque.task", "id": taskID},
		"idempotency_key": "torque-intake-" + key,
	}
	if project != "" {
		args["project"] = project
	}
	var out struct {
		ID   string `json:"id"`
		Item struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	callErr := o.call(ctx, "item_intake", taskID, args, &out)
	id := out.ID
	if id == "" {
		id = out.Item.ID
	}
	if callErr == nil && opsSlug.MatchString(id) {
		res := OpsLinkResult{OpsID: id, Source: "intake", From: key}
		return res, o.stamp(ctx, taskID, res)
	}
	msg := "invalid_item_id"
	if callErr != nil {
		msg = truncate(callErr.Error(), 300)
	}
	attempts++
	backoff := time.Duration(1<<min(attempts, 7)) * 5 * time.Second // 10s .. ~10m
	w, we := o.store.BeginWriteTx(ctx)
	if we != nil {
		return OpsLinkResult{}, we
	}
	defer w.Rollback()
	if _, we = w.Exec(`UPDATE ops_intake_pending SET attempts=?,next_at=?,last_error=? WHERE task_id=?`, attempts, o.now().Add(backoff).UTC().Format(time.RFC3339Nano), msg, taskID); we != nil {
		return OpsLinkResult{}, we
	}
	if we = w.Commit(); we != nil {
		return OpsLinkResult{}, we
	}
	return OpsLinkResult{Source: "pending", From: key}, fmt.Errorf("%w: %s", errOpsUnavailable, msg)
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

// opsTaskStatus maps a Torque task transition to the ops/v2 status state and kind
// (design 5.2: Torque posts task_started, task_review, task_done, task_blocked).
// Run and unit lifecycle events are not posted: the task transition carries them,
// and posting both produced duplicate "started" entries.
func opsTaskStatus(to string) (state, kind string) {
	switch to {
	case "doing":
		return "started", "task_started"
	case "review":
		return "note", "task_review"
	case "done":
		return "done", "task_done"
	case "blocked":
		return "blocked", "task_blocked"
	case "cancelled":
		return "note", "task_cancelled"
	}
	return "", ""
}

// deliverOps posts one task transition to the Ops service. The arguments are
// sealed before the call, so a retry after a lost response is byte-identical and
// the idempotency key (the outbox event id) dedupes it at Ops.
func (d *DispatchSync) deliverOps(ctx context.Context, entry sqlstore.OutboxEvent) error {
	ev := entry.Event
	if ev.Kind != catalog.KindTaskStateChanged {
		return nil
	}
	var p struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if e := json.Unmarshal(ev.Payload, &p); e != nil {
		return e
	}
	state, kind := opsTaskStatus(p.To)
	if state == "" {
		return nil
	}
	t, e := d.store.GetTask(ev.Scope.TaskID)
	if errors.Is(e, sqlstore.ErrTaskNotFound) {
		return nil
	}
	if e != nil {
		return e
	}
	// G1 on the transition path: hand-started agents move tasks to doing without
	// a dispatch, so link here too (inherit now, intake in the background).
	if OpsIDOf(t) == "" {
		if _, le := d.ops.Link(ctx, t.ID, false); le == nil {
			if fresh, ge := d.store.GetTask(t.ID); ge == nil {
				t = fresh
			}
		}
	}
	args := map[string]any{
		"task":            t.ID,
		"state":           state,
		"kind":            kind,
		"summary":         truncate(fmt.Sprintf("%s %s: %s", t.ID, p.To, t.Title), 500),
		"ref":             "torque://task/" + t.ID,
		"idempotency_key": "torque-status-" + ev.ID,
	}
	body := fmt.Sprintf("Torque task %s moved %s -> %s.", t.ID, p.From, p.To)
	if p.To == "blocked" && t.BlockedReason != "" {
		body += " Blocked: " + truncate(t.BlockedReason, 1000)
	}
	if ev.Scope.RunID != "" {
		body += " Run " + ev.Scope.RunID + "."
	}
	args["body"] = body
	if id := OpsIDOf(t); id != "" {
		args["ops_id"] = id
	}
	m := taskMetadata(t)
	if ws, _ := m["workstream_id"].(string); canonicalWorkstream.MatchString(ws) {
		args["workstream_id"] = ws
	} else if r, re := d.store.DispatchWorkstream(ctx, t.ID); re == nil && canonicalWorkstream.MatchString(r.ID) {
		args["workstream_id"] = r.ID
	}
	raw, e := json.Marshal(args)
	if e != nil {
		return e
	}
	raw, e = d.store.SealDispatchStatus(ctx, ev.ID, raw)
	if e != nil {
		return e
	}
	return d.ops.postStatus(ctx, raw)
}

// postStatus sends sealed status_post arguments to the Ops service.
func (o *OpsSync) postStatus(ctx context.Context, raw []byte) error {
	var args map[string]any
	if e := json.Unmarshal(raw, &args); e != nil {
		return e
	}
	task, _ := args["task"].(string)
	return o.call(ctx, "status_post", task, args, nil)
}
