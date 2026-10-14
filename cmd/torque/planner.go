package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/appdb"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/spf13/cobra"
)

func plannerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "planner",
		Short: "Portfolio planner maintenance",
	}
	cmd.AddCommand(plannerImportCmd())
	cmd.AddCommand(plannerBoardCmd())
	return cmd
}

func plannerImportCmd() *cobra.Command {
	var from string
	var apply, force, asJSON bool

	cmd := &cobra.Command{
		Use:          "import",
		SilenceUsage: true,
		Short:        "Import portfolio-tracker data/*.json into the planner",
		Long: `Import the portfolio-tracker databases into the planner, keeping ids, rev,
order, dates, comments, links and cross-references exactly.

Without --apply nothing is written: the output is the parity report (what would
be created, updated or left alone, and anything that needs a look). With --apply
the writes land in one transaction, then every written item is read back and
compared with its source.

Re-running is safe. An item already in Torque is left alone when identical,
refreshed when the source has a higher rev, and reported but not touched when
Torque's copy is newer or the two differ at the same rev; --force overwrites
those. Import does not need features.planner, but the planner stays invisible
until that flag is on.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPlannerImport(cmd.Context(), cmd.OutOrStdout(), from, apply, force, asJSON)
		},
	}
	cmd.Flags().StringVar(&from, "from", os.Getenv("PORTFOLIO_DATA_DIR"), "portfolio-tracker data directory (default $PORTFOLIO_DATA_DIR)")
	cmd.Flags().BoolVar(&apply, "apply", false, "write the import (default is a report only)")
	cmd.Flags().BoolVar(&force, "force", false, "also overwrite items whose Torque copy is newer or has diverged")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the report as JSON")
	return cmd
}

func runPlannerImport(ctx context.Context, out io.Writer, from string, apply, force, asJSON bool) error {
	if from == "" {
		return fmt.Errorf("--from is required (the portfolio-tracker data directory)")
	}
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

	svc := service.New(store)
	report, err := svc.Planner.ImportPlanner(service.PlannerImportOptions{Dir: from, Apply: apply, Force: force})
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printPlannerImportReport(out, report)
	}
	if apply && !report.Verified {
		return fmt.Errorf("import wrote data but %d item(s) do not read back equal to the source; see the report", len(report.Mismatches))
	}
	if !apply && !report.Clean() {
		return fmt.Errorf("%d item(s) need attention; see the report", len(report.Attention))
	}
	if apply && !svc.Feature.IsEnabled("planner") {
		fmt.Fprintln(out, "\nNote: features.planner is off, so the planner is not visible yet. Turn it on (settings key features.planner = true) and restart MCP.")
	}
	return nil
}

func printPlannerImportReport(out io.Writer, r *service.PlannerImportReport) {
	mode := "DRY RUN (nothing written; pass --apply to write)"
	if r.Applied {
		mode = "APPLIED"
	}
	fmt.Fprintf(out, "Planner import from %s: %s\n\n", r.Dir, mode)
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "DB\tSOURCE\tIN TORQUE\tCREATE\tUPDATE\tSAME\tTORQUE NEWER\tDIVERGED\tINVALID\tCOMMENTS\tLINKS")
	for _, d := range r.DBs {
		note := ""
		if d.Note != "" {
			note = "  (" + d.Note + ")"
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d%s\n",
			d.DB, d.Source, d.InTorque, d.Create, d.Update, d.Unchanged, d.SkipNewer, d.SkipDiverged, d.Invalid, d.CommentsInSrc, d.LinksInSrc, note)
	}
	_ = tw.Flush()
	if len(r.Attention) > 0 {
		fmt.Fprintln(out, "\nNeeds attention:")
		for _, a := range r.Attention {
			fmt.Fprintf(out, "  %s %s/%s %v %s\n", a.Action, a.DB, a.ID, a.Fields, a.Detail)
		}
	}
	if r.Applied {
		if r.Verified {
			fmt.Fprintln(out, "\nVerified: every written item reads back equal to its source.")
		} else {
			fmt.Fprintln(out, "\nNOT VERIFIED:")
			for _, m := range r.Mismatches {
				fmt.Fprintf(out, "  %s %s/%s %v %s\n", m.Action, m.DB, m.ID, m.Fields, m.Detail)
			}
		}
	}
}

func plannerBoardCmd() *cobra.Command {
	var recent, active float64
	var limit int
	var asJSON bool
	var scope string
	cmd := &cobra.Command{
		Use:          "board",
		SilenceUsage: true,
		Short:        "Print the planner departure board (in flight, landed, pre-flight, holds)",
		Long: `Print the departure board: what is in flight, what just landed, what is
preparing, and what is waiting on the owner, one row per line. By default it
lists only the workstreams tagged "board" (--scope curated); --scope all
prints the activity feed, Torque tasks included. This is the
quick check-in an agent runs at the start of a session. It reads the Torque
task store and the planner tables directly and needs features.planner.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPlannerBoard(cmd.Context(), cmd.OutOrStdout(), service.PlannerBoardOptions{RecentHours: recent, ActiveHours: active, Limit: limit, Scope: scope}, asJSON)
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "curated", "curated (workstreams tagged board) or all (the activity feed)")
	cmd.Flags().Float64Var(&recent, "recent-hours", 0, "window for landed items (default 72)")
	cmd.Flags().Float64Var(&active, "active-hours", 0, "a doing task idle longer than this is preparing (default 2)")
	cmd.Flags().IntVar(&limit, "limit", 0, "rows per section (default 30)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON instead of text")
	return cmd
}

func runPlannerBoard(ctx context.Context, out io.Writer, opts service.PlannerBoardOptions, asJSON bool) error {
	store, closeStore, err := openStoreForCLI()
	if err != nil {
		return err
	}
	defer closeStore()
	svc := service.New(store)
	board, err := svc.Planner.Board(opts)
	if err != nil {
		var fe *service.FeatureDisabledError
		if errors.As(err, &fe) {
			return fmt.Errorf("the planner is off: set features.planner = true (Settings, or PUT /api/v1/settings/features.planner)")
		}
		return err
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(board)
	}
	_, err = fmt.Fprintln(out, service.BoardText(*board))
	return err
}
