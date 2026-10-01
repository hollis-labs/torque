package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"time"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/modelcatalog"
	"github.com/hollis-labs/torque/internal/persistence/appdb"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/spf13/cobra"
)

var bootRunIDPattern = regexp.MustCompile(`-r([0-9]+)-`)

type costBackfillCandidate struct {
	TaskID           string
	RunID            int64
	Cost             float64
	PromptTokens     int
	CompletionTokens int
	AgentProfile     string
}

type sessionMeta struct {
	BootDir string `json:"torque.boot_dir"`
}

func costBackfillCmd() *cobra.Command {
	var since time.Duration
	var includePositiveUnknown bool
	var assumedClaudeModel string
	var assumedCodexModel string

	cmd := &cobra.Command{
		Use:   "cost-backfill",
		Short: "Backfill unknown cost_ledger rows from models.dev",
		Long: `Backfill unknown cost_ledger rows from models.dev.

It prices a row from its stored input and output tokens alone and labels it
models_dev. Rows written before migration 034 carry no cache token counts, so
this is a cache-unaware estimate: it overstates a runtime whose input counts
its cached tokens (codex), and Claude runs are skipped, since most of their
usage is cache and an input/output-only price would understate them several
times over. Only rows whose cost_source is unknown are candidates: a run
recorded since migration 034 with source none is not backfilled.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCostBackfill(cmd.Context(), since, includePositiveUnknown, map[string]string{
				"claude": assumedClaudeModel,
				"codex":  assumedCodexModel,
			})
		},
	}

	cmd.Flags().DurationVar(&since, "since", 7*24*time.Hour, "Only consider runs started within this lookback window")
	cmd.Flags().BoolVar(&includePositiveUnknown, "include-positive-unknown", false, "Also relabel unknown rows that already have a positive stored cost when a matching estimate can be computed")
	cmd.Flags().StringVar(&assumedClaudeModel, "assume-claude-model", "claude-sonnet-4-5", "Fallback model id for historical claude runs when the exact model snapshot was not persisted")
	cmd.Flags().StringVar(&assumedCodexModel, "assume-codex-model", "gpt-5.4", "Fallback model id for historical codex runs when the exact model snapshot was not persisted")
	return cmd
}

func runCostBackfill(ctx context.Context, since time.Duration, includePositiveUnknown bool, assumedModels map[string]string) error {
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

	profileSource, err := loadProfilesOrEmpty(cfg)
	if err != nil {
		return err
	}
	profiles := config.CurrentProfiles(profileSource)
	catalog := modelcatalog.New()
	refreshErr := catalog.Refresh(ctx)
	if refreshErr != nil && catalog.LastFetchedAt().IsZero() {
		return fmt.Errorf("models.dev refresh: %w", refreshErr)
	}
	if refreshErr != nil {
		log.Printf("[cost-backfill] models.dev refresh failed; continuing with cached catalog from %s: %v",
			catalog.LastFetchedAt().Format(time.RFC3339), refreshErr)
	}

	sinceTime := time.Now().Add(-since).UTC()
	runProviders, err := loadRunProviders(store.DB(), sinceTime)
	if err != nil {
		return fmt.Errorf("load run providers: %w", err)
	}
	candidates, err := loadCostBackfillCandidates(store.DB(), sinceTime)
	if err != nil {
		return fmt.Errorf("load candidates: %w", err)
	}

	type counters struct {
		considered         int
		updated            int
		skippedNoSession   int
		skippedNoProfile   int
		skippedProvider    int
		skippedNoModel     int
		skippedNoEstimate  int
		skippedPositive    int
		skippedNoUpdate    int
		skippedCacheHeavy  int
		relabelledPositive int
		usedAssumedModel   int
	}
	var counts counters

	tx, err := store.DB().Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, row := range candidates {
		counts.considered++

		sessionProvider, ok := runProviders[row.RunID]
		if !ok {
			counts.skippedNoSession++
			continue
		}

		profile, profileOK := profiles[row.AgentProfile]
		modelID, providerID, usedAssumed, ok := resolveBackfillModel(profileOK, profile, sessionProvider, assumedModels)
		if !ok {
			if !profileOK {
				counts.skippedNoProfile++
			} else if profile.Provider == "" || sessionProvider != profile.Provider {
				if assumedModels[sessionProvider] == "" {
					counts.skippedProvider++
				} else {
					counts.skippedNoModel++
				}
			} else {
				counts.skippedNoModel++
			}
			continue
		}
		if usedAssumed {
			counts.usedAssumedModel++
		}
		if backfillSkipsProvider(providerID) {
			counts.skippedCacheHeavy++
			continue
		}
		if row.Cost > 0 && !includePositiveUnknown {
			counts.skippedPositive++
			continue
		}

		estimated, ok := catalog.EstimateCost(
			config.CatalogProviderID(providerID),
			modelID,
			row.PromptTokens,
			row.CompletionTokens,
		)
		if !ok {
			counts.skippedNoEstimate++
			continue
		}

		result, err := tx.Exec(
			`UPDATE cost_ledger
			 SET cost = ?, cost_source = 'models_dev'
			 WHERE task_id = ? AND run_id = ? AND cost_source = 'unknown'`,
			estimated, row.TaskID, row.RunID,
		)
		if err != nil {
			return fmt.Errorf("update cost_ledger run=%d: %w", row.RunID, err)
		}
		affected, _ := result.RowsAffected()
		if affected == 0 {
			counts.skippedNoUpdate++
			continue
		}

		if row.Cost > 0 {
			counts.relabelledPositive++
		}
		counts.updated++
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	log.Printf("[cost-backfill] since=%s considered=%d updated=%d relabelled_positive=%d skipped_no_session=%d skipped_no_profile=%d skipped_provider=%d skipped_no_model=%d skipped_no_estimate=%d skipped_positive=%d skipped_no_update=%d skipped_cache_heavy=%d",
		since, counts.considered, counts.updated, counts.relabelledPositive, counts.skippedNoSession,
		counts.skippedNoProfile, counts.skippedProvider, counts.skippedNoModel, counts.skippedNoEstimate,
		counts.skippedPositive, counts.skippedNoUpdate, counts.skippedCacheHeavy)
	log.Printf("[cost-backfill] used_assumed_model=%d assumed_models=%v", counts.usedAssumedModel, assumedModels)
	return nil
}

func resolveBackfillModel(profileOK bool, profile config.AgentProfile, sessionProvider string, assumedModels map[string]string) (modelID, providerID string, usedAssumed bool, ok bool) {
	if profileOK && profile.Provider == sessionProvider && profile.Model != "" {
		return profile.Model, sessionProvider, false, true
	}
	if assumed := assumedModels[sessionProvider]; assumed != "" {
		return assumed, sessionProvider, true, true
	}
	return "", sessionProvider, false, false
}

func loadRunProviders(db *sql.DB, since time.Time) (map[int64]string, error) {
	rows, err := db.Query(
		`SELECT provider, meta
		   FROM sessions
		  WHERE created_at >= ?
		    AND task_id IS NOT NULL`,
		since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]string)
	for rows.Next() {
		var providerName string
		var metaRaw string
		if err := rows.Scan(&providerName, &metaRaw); err != nil {
			return nil, err
		}
		var meta sessionMeta
		if err := json.Unmarshal([]byte(metaRaw), &meta); err != nil {
			continue
		}
		m := bootRunIDPattern.FindStringSubmatch(meta.BootDir)
		if len(m) != 2 {
			continue
		}
		var runID int64
		if _, err := fmt.Sscan(m[1], &runID); err != nil || runID == 0 {
			continue
		}
		out[runID] = providerName
	}
	return out, rows.Err()
}

// backfillSkipsProvider reports whether a legacy ledger row for providerID is
// left unpriced. A legacy row holds input and output tokens only, and Claude's
// usage is mostly cache reads and writes, so an input/output-only price,
// labelled models_dev, would understate it several times over (run 1189: about
// $0.03 against Claude's own $0.19) and leave runs.cost out of step with the
// ledger.
func backfillSkipsProvider(providerID string) bool {
	return config.CatalogProviderID(providerID) == "anthropic"
}

func loadCostBackfillCandidates(db *sql.DB, since time.Time) ([]costBackfillCandidate, error) {
	rows, err := db.Query(
		`SELECT cl.task_id, cl.run_id, cl.cost, cl.prompt_tokens, cl.completion_tokens, t.agent_profile
		   FROM cost_ledger cl
		   JOIN tasks t ON t.id = cl.task_id
		   JOIN runs r ON r.id = cl.run_id
		  WHERE cl.cost_source = 'unknown'
		    AND (cl.prompt_tokens > 0 OR cl.completion_tokens > 0)
		    AND r.started_at >= ?
		  ORDER BY cl.run_id`,
		since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []costBackfillCandidate
	for rows.Next() {
		var row costBackfillCandidate
		if err := rows.Scan(&row.TaskID, &row.RunID, &row.Cost, &row.PromptTokens, &row.CompletionTokens, &row.AgentProfile); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
