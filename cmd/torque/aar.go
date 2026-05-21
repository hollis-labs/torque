package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/torque/internal/aar"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/appdb"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
)

// aarCmd groups the After-Action Report query / export subcommands. AARs are
// persisted as typed artifacts (Type="aar"); these subcommands provide the
// "log-folder convention + aggregation" surface the system promises agents
// and operators (CW-20260519-0088).
//
// Subcommands intentionally read straight from the local SQLite store rather
// than over HTTP so they work in CI / offline / migration paths where the
// daemon is not running. The set is small and audit-friendly: list, show,
// export.
func aarCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aar",
		Short:        "Query and export After-Action Reports filed by Torque agent runs",
		Long:         "Operate on AAR artifacts (type=\"aar\") persisted via torque_aar_submit. AARs are linked by task_id and run_id; use these subcommands to list / inspect / materialize them to disk.",
		SilenceUsage: true,
	}
	cmd.AddCommand(aarListCmd(), aarShowCmd(), aarExportCmd())
	return cmd
}

// aarListCmd renders a table of AAR artifacts. Filters compose with AND;
// time ranges are absolute or "since duration" (e.g. --since 24h). The
// default limit is 50; --all removes it.
func aarListCmd() *cobra.Command {
	var (
		taskID    string
		outcome   string
		sinceStr  string
		limit     int
		showAll   bool
		jsonOut   bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List filed AAR artifacts (newest first)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, closeFn, err := openStoreForCLI()
			if err != nil {
				return err
			}
			defer closeFn()

			filter := listFilter{
				TaskID:  taskID,
				Outcome: outcome,
				Limit:   limit,
				All:     showAll,
			}
			if sinceStr != "" {
				since, err := parseSince(sinceStr)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				filter.Since = since
			}

			rows, err := listAARs(store, filter)
			if err != nil {
				return err
			}
			if jsonOut {
				return emitJSON(cmd.OutOrStdout(), rows)
			}
			renderAARTable(cmd.OutOrStdout(), rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&taskID, "task", "", "Filter to a single task ID")
	cmd.Flags().StringVar(&outcome, "outcome", "", "Filter by outcome (success|partial|blocked|failed)")
	cmd.Flags().StringVar(&sinceStr, "since", "", "Only AARs newer than this (RFC3339 timestamp or duration like 24h, 7d)")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum rows to return (default 50)")
	cmd.Flags().BoolVar(&showAll, "all", false, "Ignore --limit and return every matching AAR")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit a JSON array of AAR rows instead of the human table")
	return cmd
}

// aarShowCmd prints the raw markdown body of one AAR (by artifact id).
// Mirrors the `gh issue view` style — one document on stdout, no envelope.
func aarShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <artifact-id>",
		Short: "Print the markdown body of an AAR artifact",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseInt64(args[0])
			if err != nil {
				return fmt.Errorf("artifact id must be an integer: %w", err)
			}
			store, closeFn, err := openStoreForCLI()
			if err != nil {
				return err
			}
			defer closeFn()

			rec, err := store.GetArtifact(id)
			if err != nil {
				if errors.Is(err, sqlstore.ErrArtifactNotFound) {
					return fmt.Errorf("artifact %d not found", id)
				}
				return err
			}
			if rec.Type != aar.ArtifactType {
				return fmt.Errorf("artifact %d has type %q, expected %q", id, rec.Type, aar.ArtifactType)
			}
			fmt.Fprint(cmd.OutOrStdout(), rec.Content)
			if !strings.HasSuffix(rec.Content, "\n") {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}
	return cmd
}

// aarExportCmd materializes matching AARs to a directory as individual
// markdown files. File naming: <task_id>-run-<run_id>.md, falling back to
// <task_id>-<artifact_id>.md when no run was attached. Existing files are
// overwritten — the AAR DB row is authoritative.
func aarExportCmd() *cobra.Command {
	var (
		dir      string
		taskID   string
		outcome  string
		sinceStr string
		limit    int
		showAll  bool
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export matching AARs as markdown files in a directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dir == "" {
				return fmt.Errorf("--dir is required")
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create dir: %w", err)
			}

			store, closeFn, err := openStoreForCLI()
			if err != nil {
				return err
			}
			defer closeFn()

			filter := listFilter{
				TaskID:  taskID,
				Outcome: outcome,
				Limit:   limit,
				All:     showAll,
			}
			if sinceStr != "" {
				since, err := parseSince(sinceStr)
				if err != nil {
					return fmt.Errorf("--since: %w", err)
				}
				filter.Since = since
			}

			rows, err := listAARs(store, filter)
			if err != nil {
				return err
			}
			for _, r := range rows {
				name := exportFilename(r)
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte(r.Content), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", path, err)
				}
				fmt.Fprintln(cmd.OutOrStdout(), path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "Output directory (required)")
	cmd.Flags().StringVar(&taskID, "task", "", "Filter to a single task ID")
	cmd.Flags().StringVar(&outcome, "outcome", "", "Filter by outcome (success|partial|blocked|failed)")
	cmd.Flags().StringVar(&sinceStr, "since", "", "Only AARs newer than this (RFC3339 timestamp or duration like 24h, 7d)")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum rows to export (default 50)")
	cmd.Flags().BoolVar(&showAll, "all", false, "Ignore --limit and export every matching AAR")
	return cmd
}

// --- internals -------------------------------------------------------------

// aarRow is the projection used by list / export. It pulls just enough
// off the artifact row (and its parsed metadata blob) to render a row +
// pick an export filename. The full markdown body is included so `export`
// can write the same content without a second SQL roundtrip.
type aarRow struct {
	ArtifactID int64     `json:"artifact_id"`
	TaskID     string    `json:"task_id"`
	RunID      int64     `json:"run_id,omitempty"`
	Outcome    string    `json:"outcome,omitempty"`
	Schema     string    `json:"schema,omitempty"`
	Populated  int       `json:"reflection_populated,omitempty"`
	ErrorsHit  int       `json:"errors_count,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	EndedAt    time.Time `json:"ended_at,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	Content    string    `json:"content,omitempty"`
}

type listFilter struct {
	TaskID  string
	Outcome string
	Since   time.Time
	Limit   int
	All     bool
}

// listAARs runs the artifact lookup with the AAR-typed filter applied. We
// pull all rows in the time window and then filter in Go to keep the SQL
// surface narrow (no per-call type-specific WHERE clause migrations); the
// AAR row volume per project is small enough that this is fine.
func listAARs(store *sqlstore.Store, f listFilter) ([]aarRow, error) {
	q := `SELECT id, task_id, run_id, type, content, metadata, created_at
		    FROM artifacts
		   WHERE type = ?`
	args := []any{aar.ArtifactType}
	if f.TaskID != "" {
		q += " AND task_id = ?"
		args = append(args, f.TaskID)
	}
	if !f.Since.IsZero() {
		q += " AND created_at >= ?"
		args = append(args, f.Since.UTC())
	}
	q += " ORDER BY created_at DESC"

	rows, err := store.ReadDB().Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []aarRow
	for rows.Next() {
		var (
			id       int64
			tid      string
			runNI    sql.NullInt64
			typ      string
			content  string
			metaNS   sql.NullString
			created  time.Time
		)
		if err := rows.Scan(&id, &tid, &runNI, &typ, &content, &metaNS, &created); err != nil {
			return nil, err
		}
		r := aarRow{
			ArtifactID: id,
			TaskID:     tid,
			CreatedAt:  created,
			Content:    content,
		}
		if runNI.Valid {
			r.RunID = runNI.Int64
		}
		if metaNS.Valid {
			var m map[string]any
			if err := json.Unmarshal([]byte(metaNS.String), &m); err == nil {
				if v, ok := m["outcome"].(string); ok {
					r.Outcome = v
				}
				if v, ok := m["schema"].(string); ok {
					r.Schema = v
				}
				if v, ok := m["reflection_populated"].(float64); ok {
					r.Populated = int(v)
				}
				if v, ok := m["errors_count"].(float64); ok {
					r.ErrorsHit = int(v)
				}
				if v, ok := m["started_at"].(string); ok {
					if t, err := time.Parse(time.RFC3339, v); err == nil {
						r.StartedAt = t
					}
				}
				if v, ok := m["ended_at"].(string); ok {
					if t, err := time.Parse(time.RFC3339, v); err == nil {
						r.EndedAt = t
					}
				}
			}
		}
		if f.Outcome != "" && r.Outcome != f.Outcome {
			continue
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// listAARs returns newest-first to match the SQL ORDER BY; sort.Stable
	// preserves SQL order while collapsing any ties (shouldn't be any —
	// created_at has sub-second resolution).
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})

	if !f.All && f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func renderAARTable(w io.Writer, rows []aarRow) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ARTIFACT\tTASK\tRUN\tOUTCOME\tSECTIONS\tERRORS\tFILED")
	for _, r := range rows {
		run := "-"
		if r.RunID != 0 {
			run = fmt.Sprintf("%d", r.RunID)
		}
		outcome := r.Outcome
		if outcome == "" {
			outcome = "-"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%d\t%s\n",
			r.ArtifactID, r.TaskID, run, outcome, r.Populated, r.ErrorsHit,
			r.CreatedAt.UTC().Format(time.RFC3339),
		)
	}
	tw.Flush()
}

// exportFilename returns the canonical on-disk name for an AAR export. Two
// formats:
//
//   <task_id>-run-<run_id>.md   (when a run_id is attached — the common case)
//   <task_id>-<artifact_id>.md  (fallback when no run_id — defensive)
//
// Task IDs already contain hyphens (e.g. CW-20260520-0007); we don't escape
// them — Torque task IDs are filename-safe by convention.
func exportFilename(r aarRow) string {
	if r.RunID != 0 {
		return fmt.Sprintf("%s-run-%d.md", r.TaskID, r.RunID)
	}
	return fmt.Sprintf("%s-%d.md", r.TaskID, r.ArtifactID)
}

func openStoreForCLI() (*sqlstore.Store, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}
	db, driver, err := appdb.Open(context.Background(), cfg.DBPath)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	if err := migrations.Run(db); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("run migrations: %w", err)
	}
	store, err := sqlstore.New(db, driver)
	if err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("create store: %w", err)
	}
	return store, func() {
		store.Close()
		db.Close()
	}, nil
}

func emitJSON(w io.Writer, rows []aarRow) error {
	// Drop body content from the JSON projection so `--json` stays terse.
	view := make([]aarRow, len(rows))
	copy(view, rows)
	for i := range view {
		view[i].Content = ""
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(view)
}

// parseSince accepts either an RFC3339 timestamp or a Go duration with one
// extra unit — `d` (days) — for ergonomics. Returns the resolved absolute
// time. "7d" → now - 7*24h; "24h" → now - 24h; "2026-05-15T00:00:00Z" → that
// exact instant.
func parseSince(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	dur, err := parseDurationDays(s)
	if err != nil {
		return time.Time{}, err
	}
	return time.Now().Add(-dur), nil
}

func parseDurationDays(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n := strings.TrimSuffix(s, "d")
		var days int64
		if _, err := fmt.Sscanf(n, "%d", &days); err != nil || days <= 0 {
			return 0, fmt.Errorf("invalid days value %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// parseInt64 parses a string into an int64 and rejects inputs with trailing
// non-numeric characters (e.g. "123abc"). strconv.ParseInt requires the
// whole string to consume successfully, unlike fmt.Sscanf("%d"), which
// stops at the first non-digit and silently accepts the prefix.
func parseInt64(s string) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(s), 10, 64)
}
