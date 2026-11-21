package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

// DispatchSyncConfig contains endpoints and credential paths, never tokens.
// A missing endpoint disables that integration. Credentials are re-read on each
// request so rotation needs no Torque restart.
type DispatchSyncConfig struct {
	RegistryURL, RegistryTokenFile   string
	TesseractURL, TesseractTokenFile string
	Author                           string
	Timeout, PollInterval            time.Duration
}

func DispatchSyncConfigFromEnv() DispatchSyncConfig {
	return DispatchSyncConfig{
		RegistryURL:        os.Getenv("TORQUE_REGISTRY_URL"),
		RegistryTokenFile:  os.Getenv("TORQUE_REGISTRY_TOKEN_FILE"),
		TesseractURL:       os.Getenv("TORQUE_TESSERACT_URL"),
		TesseractTokenFile: os.Getenv("TORQUE_TESSERACT_TOKEN_FILE"),
		Author:             os.Getenv("TORQUE_STATUS_AUTHOR"),
	}
}

// DispatchSync binds Registry identities before launch with a short deadline,
// then retries outages in the background. Its status reader consumes Torque's
// transactional outbox, advancing its durable cursor only after acceptance.
// Network I/O never happens under Torque's writer or a unit lock.
type DispatchSync struct {
	store  *sqlstore.Store
	cfg    DispatchSyncConfig
	http   *http.Client
	locks  sync.Map // task ID -> *sync.Mutex; unrelated dispatches never wait for each other
	tickMu sync.Mutex
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

const statusConsumer = "tesseract-status-v1"

var canonicalWorkstream = regexp.MustCompile(`^workstream:[0-7][0-9A-HJKMNP-TV-Z]{25}$`)
var ErrWorkstreamClosed = errors.New("Registry workstream_closed: choose a new logical workstream key")

func NewDispatchSync(store *sqlstore.Store, cfg DispatchSyncConfig) (*DispatchSync, error) {
	for _, p := range []struct{ endpoint, file string }{{cfg.RegistryURL, cfg.RegistryTokenFile}, {cfg.TesseractURL, cfg.TesseractTokenFile}} {
		if p.endpoint == "" {
			continue
		}
		u, e := url.Parse(p.endpoint)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("dispatch integration: invalid endpoint")
		}
		if p.file == "" {
			return nil, fmt.Errorf("dispatch integration: configured endpoint requires credential FILE")
		}
	}
	if cfg.TesseractURL != "" && cfg.RegistryURL == "" {
		return nil, fmt.Errorf("dispatch status requires a Registry endpoint")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 500 * time.Millisecond
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}
	if cfg.Author == "" {
		cfg.Author = "torque-dispatcher"
	}
	return &DispatchSync{store: store, cfg: cfg, http: &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Start initializes at today's head on first enable (no historical backfill).
// Existing cursors resume after restart. Call before accepting dispatches.
func (d *DispatchSync) Start(ctx context.Context) error {
	if !d.store.OutboxSupported() {
		return fmt.Errorf("dispatch integration requires the SQLite commit-ordered outbox")
	}
	if d.cfg.TesseractURL != "" {
		w, e := d.store.BeginWriteTx(ctx)
		if e != nil {
			return e
		}
		defer w.Rollback()
		_, e = w.Exec(`INSERT OR IGNORE INTO event_outbox_cursors(consumer,last_seq,updated_at) SELECT ?,MAX(COALESCE((SELECT MAX(seq) FROM event_outbox),0),(SELECT pruned_through FROM event_outbox_meta WHERE id=1)),?`, statusConsumer, time.Now().UTC().Format(time.RFC3339Nano))
		if e != nil {
			return e
		}
		if e = w.Commit(); e != nil {
			return e
		}
	}
	ctx, d.cancel = context.WithCancel(ctx)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		ticker := time.NewTicker(d.cfg.PollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e := d.Tick(ctx); e != nil && ctx.Err() == nil {
					log.Printf("[dispatch-sync] %v", e)
				}
			}
		}
	}()
	return nil
}
func dispatchLogicalKey(t *sqlstore.TaskRecord) string {
	m := taskMetadata(t)
	if p, ok := m["unit_plan"].(map[string]any); ok {
		if k, ok := p["workstream_id"].(string); ok && k != "" {
			return k
		}
	}
	for _, field := range []string{"ops_id", "workstream_key"} {
		if k, ok := m[field].(string); ok && k != "" {
			return k
		}
	}
	// Existing noncanonical Ops IDs may predate workstream_key. Never reinterpret
	// a canonical Registry id as a new logical key.
	if k, ok := m["workstream_id"].(string); ok && k != "" && !canonicalWorkstream.MatchString(k) {
		return k
	}
	if stamp, ok := m["dispatch_workstream"].(map[string]any); ok {
		if key, ok := stamp["key"].(string); ok && key != "" {
			return key
		}
	}
	return t.ID
}

// Prepare is the dispatch seam used by both units and executor dispatch. The
// returned task includes the committed stamp. Outages persist a pending mark
// and allow dispatch; closed identities are a definite refusal, never reminted.
func (d *DispatchSync) Prepare(ctx context.Context, t *sqlstore.TaskRecord) error {
	return d.prepare(ctx, t, dispatchLogicalKey(t), t.ProjectID.String)
}
func (d *DispatchSync) prepare(ctx context.Context, t *sqlstore.TaskRecord, key, project string) error {
	if d == nil || d.cfg.RegistryURL == "" {
		return nil
	}
	lock := d.taskLock(t.ID)
	lock.Lock()
	defer lock.Unlock()
	registryProject, namespace := d.projectScope(project)
	r, e := d.store.ReserveDispatchWorkstream(ctx, t.ID, key, project, registryProject, namespace)
	if e != nil {
		return e
	}
	e = d.ensure(ctx, &r)
	if errors.Is(e, ErrWorkstreamClosed) {
		return e
	}
	if e != nil {
		return e
	} // local persistence errors only; upstream outages are marked
	fresh, e := d.store.GetTask(t.ID)
	if e == nil {
		*t = *fresh
	}
	return e
}
func (d *DispatchSync) ensure(ctx context.Context, r *sqlstore.DispatchWorkstream) error {
	if r.State == "closed" {
		return ErrWorkstreamClosed
	}
	if r.ProjectID != "" && r.RegistryProject == "" {
		r.RegistryProject, r.NamespaceProject = d.projectScope(r.ProjectID)
		if r.RegistryProject == "" {
			r.State = "pending"
			r.LastError = "project_unmapped"
			return d.store.SaveDispatchWorkstream(ctx, *r)
		}
	}
	body := map[string]any{"key": r.Key}
	if r.RegistryProject != "" {
		body["project_urns"] = []string{r.RegistryProject}
	}
	retry := sha256.Sum256([]byte(r.RegistryProject + "\x00" + r.Key))
	var result struct {
		ID string `json:"id"`
	}
	code, e := d.post(ctx, d.cfg.RegistryURL, "/v1/workstreams/ensure", d.cfg.RegistryTokenFile, "torque-ensure-"+hex.EncodeToString(retry[:]), body, &result)
	r.LastError = ""
	switch {
	case code == "workstream_closed":
		r.State = "closed"
		r.LastError = "workstream_closed"
	case e != nil:
		r.State = "pending"
		r.LastError = code
		if r.LastError == "" {
			r.LastError = "unavailable"
		}
	case !canonicalWorkstream.MatchString(result.ID):
		r.State = "pending"
		r.LastError = "invalid_registry_id"
	default:
		r.ID = result.ID
		r.State = "ensured"
	}
	if err := d.store.SaveDispatchWorkstream(ctx, *r); err != nil {
		if errors.Is(err, sqlstore.ErrDispatchWorkstreamClosed) {
			return ErrWorkstreamClosed
		}
		return err
	}
	if r.State == "closed" {
		return ErrWorkstreamClosed
	}
	return nil
}

// post deliberately never returns response bodies or credentials in errors.
func (d *DispatchSync) post(ctx context.Context, endpoint, path, file, key string, body, result any) (string, error) {
	token, e := os.ReadFile(file)
	if e != nil {
		return "credential_unavailable", errors.New("credential file unavailable")
	}
	credential := strings.TrimSpace(string(token))
	if credential == "" {
		return "credential_unavailable", errors.New("credential file empty")
	}
	raw, e := json.Marshal(body)
	if e != nil {
		return "invalid_request", e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+path, bytes.NewReader(raw))
	if e != nil {
		return "invalid_endpoint", errors.New("invalid endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, e := d.http.Do(req)
	if e != nil {
		return "unavailable", errors.New("upstream unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var wire struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&wire)
		code := "upstream_rejected"
		if wire.Error.Code == "workstream_closed" {
			code = "workstream_closed"
		}
		return code, fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}
	if result != nil {
		if e = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result); e != nil {
			return "invalid_response", errors.New("invalid upstream response")
		}
	}
	return "", nil
}

// Tick retries pending Registry bindings and delivers a bounded outbox page.
// A failed delivery retains the cursor and the same idempotency key. This is
// at-least-once delivery, including the accepted-but-response-lost case.
func (d *DispatchSync) Tick(ctx context.Context) error {
	d.tickMu.Lock()
	defer d.tickMu.Unlock()
	if d.cfg.RegistryURL != "" {
		pending, e := d.store.PendingDispatchWorkstreams(ctx)
		if e != nil {
			return e
		}
		for i := range pending {
			lock := d.taskLock(pending[i].TaskID)
			lock.Lock()
			current, e := d.store.DispatchWorkstreamForKey(ctx, pending[i])
			if e == nil && current.State == "pending" {
				e = d.ensure(ctx, &current)
			}
			lock.Unlock()
			if e != nil && !errors.Is(e, ErrWorkstreamClosed) {
				return e
			}
		}
	}
	if d.cfg.TesseractURL == "" {
		return nil
	}
	after, e := d.store.OutboxCursorLoad(ctx, statusConsumer)
	if e != nil {
		return e
	}
	head, e := d.store.OutboxHead(ctx)
	if e != nil {
		return e
	}
	if head < after {
		return fmt.Errorf("status cursor is beyond outbox head: restore/reset requires reconciliation")
	}
	events, e := d.store.OutboxAfter(ctx, after, 100)
	if e != nil {
		return e
	}
	for _, entry := range events {
		if e = d.deliver(ctx, entry); e != nil {
			return e
		}
		if e = d.store.OutboxCursorSave(ctx, statusConsumer, entry.Seq); e != nil {
			return e
		}
	}
	return nil
}
func (d *DispatchSync) deliver(ctx context.Context, entry sqlstore.OutboxEvent) error {
	ev := entry.Event
	var sealed string
	if err := d.store.ReadDB().QueryRowContext(ctx, `SELECT payload FROM dispatch_status_payloads WHERE event_id=?`, ev.ID).Scan(&sealed); err == nil {
		_, err = d.post(ctx, d.cfg.TesseractURL, "/v1/event/write", d.cfg.TesseractTokenFile, "torque-status-"+ev.ID, json.RawMessage(sealed), nil)
		return err
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if ev.Scope == nil || ev.Scope.TaskID == "" {
		return nil
	}
	var p struct {
		State        string `json:"state"`
		ResultState  string `json:"result_state"`
		To           string `json:"to"`
		RunID        string `json:"run_id"`
		WorkstreamID string `json:"workstream_id"`
		DispatchKey  string `json:"dispatch_key"`
	}
	if e := json.Unmarshal(ev.Payload, &p); e != nil {
		return e
	}
	state := ""
	switch ev.Kind {
	case "unit.lifecycle":
		switch p.State {
		case "dispatched", "formed":
			state = "started"
		case "running":
			state = "progress"
		case "RESULT":
			state = p.ResultState
			if state != "done" && state != "failed" && state != "blocked" {
				state = "done"
			}
		case "dissolved":
			state = "note"
		case "failed", "blocked", "cancelled":
			state = p.State
		}
	case catalog.KindTaskStateChanged:
		switch p.To {
		case "doing":
			state = "progress"
		case "review", "blocked", "done", "cancelled":
			state = p.To
		}
	case catalog.KindRunStateChanged:
		switch p.To {
		case "running":
			state = "progress"
		case "failed", "error":
			state = "failed"
		case "blocked":
			state = "blocked"
		case "cancelled", "canceled", "killed":
			state = "cancelled"
		}
	}
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
	isUnit := HasUnitPlan(*t)
	var sealedUnit bool
	if err := d.store.ReadDB().QueryRowContext(ctx, `SELECT is_unit FROM dispatch_event_workstreams WHERE event_id=?`, ev.ID).Scan(&sealedUnit); err == nil {
		isUnit = sealedUnit
	}
	if ev.Kind == catalog.KindTaskStateChanged && p.To == "doing" && !isUnit {
		state = "started"
	}
	r, e := d.store.DispatchEventWorkstream(ctx, ev.ID)
	if errors.Is(e, sql.ErrNoRows) {
		if e = d.Prepare(ctx, t); e != nil && !errors.Is(e, ErrWorkstreamClosed) {
			return e
		}
		r, e = d.store.DispatchWorkstream(ctx, t.ID)
	}
	if e != nil {
		return e
	}
	if r.State == "closed" && r.ID == "" {
		return nil
	}
	if r.State != "ensured" && r.State != "closed" {
		return fmt.Errorf("status delivery pending Registry for task %s", t.ID)
	}
	ws := ev.Scope.WorkstreamID
	if ws == "" {
		ws = p.WorkstreamID
	}
	if ws == "" {
		ws = r.ID
	}
	runID := p.RunID
	if runID == "" {
		runID = ev.Scope.RunID
	}
	if runID == "" && p.DispatchKey == "" {
		_ = d.store.ReadDB().QueryRowContext(ctx, `SELECT unit_run_id FROM dispatch_event_workstreams WHERE event_id=?`, ev.ID).Scan(&runID)
	}
	if runID == "" && p.DispatchKey != "" {
		_ = d.store.ReadDB().QueryRowContext(ctx, `SELECT json_extract(envelope,'$.payload.run_id') FROM event_outbox WHERE task_id=? AND kind='unit.lifecycle' AND json_extract(envelope,'$.payload.dispatch_key')=? AND COALESCE(json_extract(envelope,'$.payload.run_id'),'')<>'' ORDER BY seq LIMIT 1`, t.ID, p.DispatchKey).Scan(&runID)
	}
	if runID == "" && ev.Kind == catalog.KindTaskStateChanged && !isUnit {
		var executorRun sql.NullInt64
		direction, order := "<", "DESC"
		if p.To == "doing" {
			direction, order = ">", "ASC"
		}
		query := `SELECT run_id FROM event_outbox WHERE task_id=? AND seq` + direction + `? AND kind=? AND run_id IS NOT NULL ORDER BY seq ` + order + ` LIMIT 1`
		if err := d.store.ReadDB().QueryRowContext(ctx, query, t.ID, entry.Seq, catalog.KindRunStateChanged).Scan(&executorRun); err == nil && executorRun.Valid {
			runID = fmt.Sprintf("%d", executorRun.Int64)
		}
	}
	if isUnit && runID == "" && p.State == "dispatched" {
		if unit, ok := unitRunFrom(taskMetadata(t)); ok && unit.Key == p.DispatchKey && (unit.State == UnitForming || unit.State == UnitActive) {
			return fmt.Errorf("status delivery awaiting team run id for task %s", t.ID)
		}
	}
	sessionID := ev.SessionID
	if sessionID == "" && runID != "" && !isUnit {
		var session sql.NullString
		_ = d.store.ReadDB().QueryRowContext(ctx, `SELECT tether_session_id FROM runs WHERE CAST(id AS TEXT)=? AND task_id=?`, runID, t.ID).Scan(&session)
		sessionID = session.String
	}

	project := r.NamespaceProject
	if project == "" {
		project = "agent-os"
	}
	key := "torque-status-" + ev.ID
	body := map[string]any{
		"namespace":     "project/" + project + "/event/status",
		"workstream_id": ws,
		"summary":       fmt.Sprintf("%s %s", t.ID, state),
		"body":          "Torque dispatch transition: " + state,
		"author":        map[string]any{"agent_id": d.cfg.Author},
		"session_id":    sessionID,
		"data": map[string]any{
			"task_id": t.ID, "run_id": runID, "workstream_id": ws,
			"event_id": ev.ID, "dispatch_key": p.DispatchKey, "transition": p.State,
		},
		"consumer_state": map[string]any{
			"state": state, "subject_urn": "msg://task/torque/" + t.ID,
			"task": t.ID, "run": runID,
			"task_id": t.ID, "run_id": runID, "workstream_key": r.Key,
		},
		"idempotency_key": key,
	}
	if isUnit && runID != "" {
		body["consumer_state"].(map[string]any)["team_id"] = runID
	}
	raw, e := json.Marshal(body)
	if e != nil {
		return e
	}
	raw, e = d.store.SealDispatchStatus(ctx, ev.ID, raw)
	if e != nil {
		return e
	}
	_, e = d.post(ctx, d.cfg.TesseractURL, "/v1/event/write", d.cfg.TesseractTokenFile, key, json.RawMessage(raw), nil)
	return e
}

func (d *DispatchSync) taskLock(taskID string) *sync.Mutex {
	v, _ := d.locks.LoadOrStore(taskID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// reserve seals a binding before the first durable dispatch event, without I/O.
func (d *DispatchSync) reserve(ctx context.Context, t *sqlstore.TaskRecord, key, project string) error {
	if d == nil || d.cfg.RegistryURL == "" {
		return nil
	}
	registryProject, namespace := d.projectScope(project)
	_, err := d.store.ReserveDispatchWorkstream(ctx, t.ID, key, project, registryProject, namespace)
	return err
}

// Stop cancels and joins the background reader before the store is closed.
func (d *DispatchSync) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	d.wg.Wait()
}

// Local PRJ IDs are storage keys, never Registry domains or namespace slugs.
func (d *DispatchSync) projectScope(localID string) (registryProject, namespace string) {
	namespace = "agent-os"
	if localID == "" {
		return
	}
	p, err := d.store.GetProject(localID)
	if err != nil {
		return
	}
	if p.ExternalAuthority == "registry" && strings.HasPrefix(p.ExternalID, "project:") {
		slug := strings.TrimPrefix(p.ExternalID, "project:")
		if projectSlug.MatchString(slug) {
			return p.ExternalID, slug
		}
	}
	if projectSlug.MatchString(p.Name) {
		namespace = p.Name
	}
	return
}

var projectSlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
