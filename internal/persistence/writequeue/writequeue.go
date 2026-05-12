package writequeue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	queue "github.com/hollis-labs/go-queue"
	qsqlite "github.com/hollis-labs/go-queue/driver/sqlite"
	"github.com/hollis-labs/go-sqlite/sqlitekit"
)

const (
	jobTypeRunEvent = "run_event"
	jobTypeCost     = "cost_ledger"
	jobTypeComment  = "comment"
)

// TelemetryWriter routes telemetry-class writes either directly to the main
// DB or through queue.db-backed async drainers.
type TelemetryWriter interface {
	AppendRunEvent(ctx context.Context, evt *sqlstore.RunEventRecord) error
	RecordCost(ctx context.Context, rec *sqlstore.CostLedgerRecord) error
	AddComment(ctx context.Context, rec *sqlstore.CommentRecord) error
	Depth(ctx context.Context) (int, error)
}

// Config configures the telemetry write queue.
type Config struct {
	QueueName    string
	JobsTable    string
	FailedTable  string
	PollInterval time.Duration
	RetryAfter   time.Duration
	Concurrency  int
	MaxTries     int
}

// DefaultConfig returns conservative defaults for queue.db telemetry writes.
func DefaultConfig() Config {
	return Config{
		QueueName:    "telemetry",
		JobsTable:    "telemetry_jobs",
		FailedTable:  "telemetry_failed_jobs",
		PollInterval: 100 * time.Millisecond,
		RetryAfter:   250 * time.Millisecond,
		Concurrency:  1,
		MaxTries:     5,
	}
}

// OpenDB opens the dedicated queue.db connection used by the write queue.
//
// The handle is a single-connection writer pool: SQLite serializes writes and
// pinning to one conn makes SQLITE_BUSY impossible from within this process.
// WAL, busy_timeout, synchronous=NORMAL, and temp_store=memory are applied via
// the DSN by sqlitekit so every (re)connection inherits them.
func OpenDB(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sqlitekit.OpenSingle(ctx, path, sqlitekit.OpenOptions{
		Options:         sqlitekit.WriterOptions(),
		CreateParentDir: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open writequeue db: %w", err)
	}
	return db, nil
}

type runEventJob struct {
	RunID   *int64 `json:"run_id,omitempty"`
	TaskID  string `json:"task_id"`
	Type    string `json:"type"`
	Payload string `json:"payload"`
}

type costJob struct {
	TaskID           string  `json:"task_id"`
	RunID            int64   `json:"run_id"`
	SprintID         string  `json:"sprint_id,omitempty"`
	Cost             float64 `json:"cost"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostSource       string  `json:"cost_source"`
}

type commentJob struct {
	RequestID  string `json:"request_id,omitempty"`
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Author     string `json:"author"`
	Content    string `json:"content"`
}

type jobResult struct {
	comment sqlstore.CommentRecord
	err     error
}

// Direct is the synchronous fallback used by tests and narrow callsites that
// need existing direct-write behavior.
type Direct struct {
	store *sqlstore.Store
}

// NewDirect returns a direct-write telemetry sink.
func NewDirect(store *sqlstore.Store) *Direct {
	return &Direct{store: store}
}

func (d *Direct) AppendRunEvent(_ context.Context, evt *sqlstore.RunEventRecord) error {
	_, err := d.store.AppendRunEvent(evt)
	return err
}

func (d *Direct) RecordCost(_ context.Context, rec *sqlstore.CostLedgerRecord) error {
	_, err := d.store.AppendCostLedger(rec)
	return err
}

func (d *Direct) AddComment(_ context.Context, rec *sqlstore.CommentRecord) error {
	return d.store.AddComment(rec)
}

func (d *Direct) Depth(context.Context) (int, error) {
	return 0, nil
}

// Writer routes telemetry-class writes through go-queue backed by queue.db.
type Writer struct {
	store  *sqlstore.Store
	db     *sql.DB
	driver queue.Queue
	worker *queue.Worker
	cfg    Config

	nextRequestID atomic.Uint64

	pendingMu sync.Mutex
	pending   map[string]chan jobResult
}

// New constructs a queue-backed telemetry writer.
func New(store *sqlstore.Store, queueDB *sql.DB, cfg Config) (*Writer, error) {
	def := DefaultConfig()
	if cfg.QueueName == "" {
		cfg.QueueName = def.QueueName
	}
	if cfg.JobsTable == "" {
		cfg.JobsTable = def.JobsTable
	}
	if cfg.FailedTable == "" {
		cfg.FailedTable = def.FailedTable
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = def.PollInterval
	}
	if cfg.RetryAfter <= 0 {
		cfg.RetryAfter = def.RetryAfter
	}
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = def.Concurrency
	}
	if cfg.MaxTries <= 0 {
		cfg.MaxTries = def.MaxTries
	}

	driver, err := qsqlite.New(queueDB, qsqlite.Opts{
		Table:       cfg.JobsTable,
		FailedTable: cfg.FailedTable,
		RetryAfter:  cfg.RetryAfter,
	})
	if err != nil {
		return nil, fmt.Errorf("create telemetry queue driver: %w", err)
	}

	w := &Writer{
		store:   store,
		db:      queueDB,
		driver:  driver,
		cfg:     cfg,
		pending: make(map[string]chan jobResult),
	}
	w.worker = queue.NewWorker(driver, queue.WorkerOpts{
		Queues:       []string{cfg.QueueName},
		Concurrency:  cfg.Concurrency,
		PollInterval: cfg.PollInterval,
		MaxTries:     cfg.MaxTries,
		RetryAfter:   cfg.RetryAfter,
		OnError: func(err error) {
			log.Printf("[writequeue] worker error: %v", err)
		},
		OnFailed: func(job *queue.QueuedJob, err error) {
			log.Printf("[writequeue] job failed type=%s id=%s attempts=%d: %v", job.Type, job.ID, job.Attempts, err)
		},
	})
	w.worker.Register(jobTypeRunEvent, w.handleRunEvent)
	w.worker.Register(jobTypeCost, w.handleCost)
	w.worker.Register(jobTypeComment, w.handleComment)

	return w, nil
}

// Start runs the queue worker until ctx is cancelled.
func (w *Writer) Start(ctx context.Context) error {
	return w.worker.Start(ctx)
}

// Close closes the queue.db handle.
func (w *Writer) Close() error {
	return w.db.Close()
}

// Depth reports the current telemetry queue depth.
func (w *Writer) Depth(ctx context.Context) (int, error) {
	return w.driver.Size(ctx, w.cfg.QueueName)
}

// AppendRunEvent enqueues a run event write.
func (w *Writer) AppendRunEvent(ctx context.Context, evt *sqlstore.RunEventRecord) error {
	if evt == nil {
		return fmt.Errorf("run event is nil")
	}
	payload := runEventJob{
		TaskID:  evt.TaskID,
		Type:    evt.Type,
		Payload: evt.Payload,
	}
	if evt.RunID.Valid {
		runID := evt.RunID.Int64
		payload.RunID = &runID
	}
	return w.push(ctx, jobTypeRunEvent, payload)
}

// RecordCost enqueues a cost_ledger write.
func (w *Writer) RecordCost(ctx context.Context, rec *sqlstore.CostLedgerRecord) error {
	if rec == nil {
		return fmt.Errorf("cost record is nil")
	}
	return w.push(ctx, jobTypeCost, costJob{
		TaskID:           rec.TaskID,
		RunID:            rec.RunID,
		SprintID:         rec.SprintID,
		Cost:             rec.Cost,
		PromptTokens:     rec.PromptTokens,
		CompletionTokens: rec.CompletionTokens,
		CostSource:       rec.CostSource,
	})
}

// AddComment enqueues a comment write and waits for the persisted row.
func (w *Writer) AddComment(ctx context.Context, rec *sqlstore.CommentRecord) error {
	if rec == nil {
		return fmt.Errorf("comment is nil")
	}
	requestID, ch := w.registerPending()
	defer w.unregisterPending(requestID)

	err := w.push(ctx, jobTypeComment, commentJob{
		RequestID:  requestID,
		EntityType: rec.EntityType,
		EntityID:   rec.EntityID,
		Author:     rec.Author,
		Content:    rec.Content,
	})
	if err != nil {
		return err
	}

	select {
	case res := <-ch:
		if res.err != nil {
			return res.err
		}
		*rec = res.comment
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) push(ctx context.Context, jobType string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s payload: %w", jobType, err)
	}
	if err := w.driver.Push(ctx, jobType, body, queue.OnQueue(w.cfg.QueueName)); err != nil {
		return fmt.Errorf("enqueue %s: %w", jobType, err)
	}
	return nil
}

func (w *Writer) handleRunEvent(ctx context.Context, job *queue.QueuedJob) error {
	var payload runEventJob
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshal run event: %w", err)
	}
	rec := &sqlstore.RunEventRecord{
		TaskID:  payload.TaskID,
		Type:    payload.Type,
		Payload: payload.Payload,
	}
	if payload.RunID != nil {
		rec.RunID = sql.NullInt64{Int64: *payload.RunID, Valid: true}
	}
	_, err := w.store.AppendRunEvent(rec)
	return err
}

func (w *Writer) handleCost(ctx context.Context, job *queue.QueuedJob) error {
	var payload costJob
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshal cost: %w", err)
	}
	_, err := w.store.AppendCostLedger(&sqlstore.CostLedgerRecord{
		TaskID:           payload.TaskID,
		RunID:            payload.RunID,
		SprintID:         payload.SprintID,
		Cost:             payload.Cost,
		PromptTokens:     payload.PromptTokens,
		CompletionTokens: payload.CompletionTokens,
		CostSource:       payload.CostSource,
	})
	return err
}

func (w *Writer) handleComment(ctx context.Context, job *queue.QueuedJob) error {
	var payload commentJob
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("unmarshal comment: %w", err)
	}
	rec := sqlstore.CommentRecord{
		EntityType: payload.EntityType,
		EntityID:   payload.EntityID,
		Author:     payload.Author,
		Content:    payload.Content,
	}
	err := w.store.AddComment(&rec)
	w.resolvePending(payload.RequestID, jobResult{comment: rec, err: err})
	return err
}

func (w *Writer) registerPending() (string, chan jobResult) {
	id := strconv.FormatUint(w.nextRequestID.Add(1), 10)
	ch := make(chan jobResult, 1)
	w.pendingMu.Lock()
	w.pending[id] = ch
	w.pendingMu.Unlock()
	return id, ch
}

func (w *Writer) unregisterPending(id string) {
	w.pendingMu.Lock()
	delete(w.pending, id)
	w.pendingMu.Unlock()
}

func (w *Writer) resolvePending(id string, res jobResult) {
	if id == "" {
		return
	}
	w.pendingMu.Lock()
	ch := w.pending[id]
	w.pendingMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- res:
	default:
	}
}
