package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/appdb"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
)

// fkOrphanColumn describes one of the three soft (Go-checked-only) foreign
// key columns on `tasks` targeted by FK-001 (ADR-0004 §3 "Real foreign
// keys"). FK-002 will promote these to real `REFERENCES ... ON DELETE SET
// NULL` columns; this command is the prerequisite cleanup pass that makes
// that migration safe to run (SQLite's `PRAGMA foreign_key_check`, which
// migrations.Run enforces after every step, would otherwise fail the first
// time a stale reference is hit).
type fkOrphanColumn struct {
	// Column is the tasks.* column holding the soft reference.
	Column string
	// RefTable is the table the column is expected to reference by id.
	RefTable string
	// Label is a human-readable noun for log output.
	Label string
}

var fkOrphanColumns = []fkOrphanColumn{
	{Column: "sprint_id", RefTable: "sprints", Label: "sprint"},
	{Column: "project_id", RefTable: "projects", Label: "project"},
	{Column: "epic_id", RefTable: "epics", Label: "epic"},
}

// fkOrphanRef is one distinct dangling id value found in a tasks column,
// with how many task rows point at it.
type fkOrphanRef struct {
	Value string
	Count int
}

func fkOrphanCleanupCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "fk-orphan-cleanup",
		Short: "Find and null out tasks.sprint_id/project_id/epic_id rows that reference a deleted sprint/project/epic",
		Long: strings.TrimSpace(`
Finds every tasks row where sprint_id, project_id, or epic_id is non-null
but points at a sprint/project/epic that no longer exists, and sets the
dangling column to NULL — matching the ON DELETE SET NULL behavior FK-002
will enforce once it adds real REFERENCES constraints on these columns.

Safe to re-run: once orphans are cleaned, a subsequent run finds zero rows.
Use --dry-run to see counts (and any suspicious single-id clusters) without
writing anything.
`),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFKOrphanCleanup(cmd.Context(), dryRun)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Report orphaned references without modifying the database")
	return cmd
}

func runFKOrphanCleanup(ctx context.Context, dryRun bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	db, driver, err := appdb.Open(ctx, cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	if err := migrations.Run(db); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	store, err := sqlstore.New(db, driver)
	if err != nil {
		return fmt.Errorf("create store: %w", err)
	}
	defer store.Close()

	findings := make(map[string][]fkOrphanRef, len(fkOrphanColumns))
	total := 0
	for _, oc := range fkOrphanColumns {
		refs, n, err := findFKOrphans(store.DB(), oc)
		if err != nil {
			return fmt.Errorf("find orphaned %s: %w", oc.Column, err)
		}
		findings[oc.Column] = refs
		total += n
		logFKOrphanReport(oc, refs, n)
	}

	if total == 0 {
		log.Printf("[fk-orphan-cleanup] no orphaned sprint_id/project_id/epic_id references found; nothing to do")
		return nil
	}

	if dryRun {
		log.Printf("[fk-orphan-cleanup] dry-run: %d orphaned reference(s) found total; no changes made (re-run without --dry-run to clean up)", total)
		return nil
	}

	tx, err := store.DB().Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var cleaned []string
	for _, oc := range fkOrphanColumns {
		n, err := nullifyFKOrphans(tx, oc)
		if err != nil {
			return fmt.Errorf("clean up orphaned %s: %w", oc.Column, err)
		}
		cleaned = append(cleaned, fmt.Sprintf("%s=%d", oc.Column, n))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	log.Printf("[fk-orphan-cleanup] cleaned orphaned references: %s (total=%d)", strings.Join(cleaned, " "), total)
	return nil
}

// findFKOrphans returns every distinct dangling id value in tasks.<column>
// (i.e. non-null but absent from <ref-table>.id), with how many task rows
// carry each one, plus the total row count across all values.
func findFKOrphans(db *sql.DB, oc fkOrphanColumn) ([]fkOrphanRef, int, error) {
	query := fmt.Sprintf(`
		SELECT t.%s AS ref, COUNT(*) AS n
		FROM tasks t
		WHERE t.%s IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM %s r WHERE r.id = t.%s)
		GROUP BY t.%s
		ORDER BY n DESC, ref ASC`,
		oc.Column, oc.Column, oc.RefTable, oc.Column, oc.Column)

	rows, err := db.Query(query)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []fkOrphanRef
	total := 0
	for rows.Next() {
		var ref fkOrphanRef
		if err := rows.Scan(&ref.Value, &ref.Count); err != nil {
			return nil, 0, err
		}
		out = append(out, ref)
		total += ref.Count
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// nullifyFKOrphans sets tasks.<column> to NULL for every row whose value is
// non-null but absent from <ref-table>.id. Returns rows affected.
func nullifyFKOrphans(tx *sql.Tx, oc fkOrphanColumn) (int64, error) {
	query := fmt.Sprintf(`
		UPDATE tasks
		SET %s = NULL
		WHERE %s IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM %s r WHERE r.id = tasks.%s)`,
		oc.Column, oc.Column, oc.RefTable, oc.Column)

	res, err := tx.Exec(query)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// logFKOrphanReport logs a summary of orphans found for one column,
// including the top dangling id values. It also flags — without changing
// behavior — when orphans are suspiciously concentrated on a single missing
// id (>= 3 rows, >= half of the column's total orphans), since that pattern
// looks more like "one deletion left a batch of stale references behind" and
// is worth a human confirming was intentional data hygiene rather than a
// bug elsewhere (e.g. a delete path that skipped cleaning up its own
// children) before leaning on this cleanup run after run.
func logFKOrphanReport(oc fkOrphanColumn, refs []fkOrphanRef, total int) {
	if total == 0 {
		log.Printf("[fk-orphan-cleanup] %s: no orphans found", oc.Column)
		return
	}

	log.Printf("[fk-orphan-cleanup] %s: %d orphaned task row(s) across %d distinct missing %s id(s)",
		oc.Column, total, len(refs), oc.Label)

	shown := refs
	if len(shown) > 3 {
		shown = shown[:3]
	}
	for _, r := range shown {
		log.Printf("[fk-orphan-cleanup]   %s=%q -> %d task(s)", oc.Column, r.Value, r.Count)
	}
	if len(refs) > len(shown) {
		log.Printf("[fk-orphan-cleanup]   ... and %d more distinct missing %s id(s)", len(refs)-len(shown), oc.Label)
	}

	top := refs[0]
	if total >= 3 && float64(top.Count)/float64(total) >= 0.5 {
		log.Printf("[fk-orphan-cleanup]   WARNING: %d/%d orphaned %s reference(s) (%.0f%%) all point at the single missing id %q — looks like a cluster from one deletion rather than scattered staleness; confirm that deletion was expected before treating this cleanup as routine",
			top.Count, total, oc.Column, 100*float64(top.Count)/float64(total), top.Value)
	}
}
