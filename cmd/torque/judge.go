package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/hollis-labs/torque/internal/judge"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/spf13/cobra"
)

func judgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "judge",
		Short: "Structured judgments: question sets, evaluation and the enrichment ledger",
		Long: `Structured judgments classify, tag and enrich tasks: a model answers yes/no,
pick-one and rate-on-a-scale questions with probabilities, Torque owns the
questions and thresholds, and every answer is recorded as a suggestion in a
ledger. Nothing here changes a task.

Only the keyword baseline decider is available until Tether's Decide operation
and the JEV adapter exist (CW-20261010-0041). The baseline is the floor a real
model has to beat.`,
	}
	cmd.AddCommand(judgeSetsCmd(), judgeEvalCmd(), judgeEnrichCmd(), judgeLedgerCmd())
	return cmd
}

func decider(name string) (judge.Decider, error) {
	switch name {
	case "baseline", "keyword-baseline":
		return judge.Baseline{}, nil
	case "jev":
		return nil, fmt.Errorf("the jev decider needs Tether's Decide operation and the JEV adapter (CW-20261010-0041); use --decider baseline until then")
	}
	return nil, fmt.Errorf("unknown decider %q (available: baseline)", name)
}

func withJudge(fn func(ctx context.Context, svc *service.Service, out io.Writer) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		store, closeStore, err := openStoreForCLI()
		if err != nil {
			return err
		}
		defer closeStore()
		return fn(cmd.Context(), service.New(store), cmd.OutOrStdout())
	}
}

func judgeSetsCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "sets",
		Short:        "List the question sets",
		SilenceUsage: true,
		RunE: withJudge(func(ctx context.Context, svc *service.Service, out io.Writer) error {
			sets, err := svc.Judge.Sets()
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "SET\tVERSION\tHASH\tPURPOSE\tQUESTIONS")
			for _, id := range judge.SetIDs(sets) {
				s := sets[id]
				var qs []string
				for _, q := range s.Questions {
					qs = append(qs, fmt.Sprintf("%s (%s, suggest>=%.2f)", q.Key, q.Type, q.SuggestMin))
				}
				fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", s.ID, s.Version, s.Hash(), s.Purpose, strings.Join(qs, "; "))
			}
			return tw.Flush()
		}),
	}
}

func judgeEvalCmd() *cobra.Command {
	var set, decName string
	var n int
	var seed int64
	var noFlips bool
	cmd := &cobra.Command{
		Use:          "eval",
		Short:        "Evaluate a question set against the tags tasks already carry",
		SilenceUsage: true,
		Long: `Draws labeled examples from the tags tasks already carry (see the set's
label_tags), asks the decider the set's questions about each, and reports per
question: accuracy, accuracy by confidence band, what acting above each
confidence threshold would cover and get right, option-order flips, and the most
confident disagreements.

Existing tags are real but noisy labels, so review the disagreements before
trusting a number. A Choice samples the natural mix of kinds; a yes/no question
samples balanced positives and negatives, so its accuracy is on that mix.
Tasks with no tags at all are never used as negatives.`,
		RunE: withJudge(func(ctx context.Context, svc *service.Service, out io.Writer) error {
			d, err := decider(decName)
			if err != nil {
				return err
			}
			rep, err := svc.Judge.Evaluate(ctx, set, d, service.JudgeEvalOptions{N: n, Seed: seed, NoFlips: noFlips})
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Sample: up to %d labeled examples per question, seed %d, from the tags tasks already carry.\n\n%s", n, seed, rep.Text())
			return nil
		}),
	}
	cmd.Flags().StringVar(&set, "set", "", "question set id (see `torque judge sets`)")
	cmd.Flags().StringVar(&decName, "decider", "baseline", "decider: baseline")
	cmd.Flags().IntVar(&n, "n", 200, "labeled examples per question")
	cmd.Flags().Int64Var(&seed, "seed", 1, "sampling seed (the same seed draws the same sample)")
	cmd.Flags().BoolVar(&noFlips, "no-flips", false, "skip the reordered second ask (halves the calls)")
	_ = cmd.MarkFlagRequired("set")
	return cmd
}

func judgeEnrichCmd() *cobra.Command {
	var set, task, decName string
	var record bool
	cmd := &cobra.Command{
		Use:          "enrich",
		Short:        "Ask a set's questions about one task and show (or record) the suggestions",
		SilenceUsage: true,
		RunE: withJudge(func(ctx context.Context, svc *service.Service, out io.Writer) error {
			d, err := decider(decName)
			if err != nil {
				return err
			}
			recs, err := svc.Judge.EnrichTask(ctx, d, set, task, record)
			if err != nil {
				return err
			}
			for _, r := range recs {
				answer := r.Choice
				if r.Noul != nil {
					answer = fmt.Sprintf("p(yes)=%.2f", *r.Noul)
				}
				fmt.Fprintf(out, "%-16s %-14s confidence %.2f  suggests %v\n", r.QuestionKey, answer, r.Confidence, r.Suggested)
			}
			if record {
				fmt.Fprintf(out, "\nRecorded as suggestions by %s. Nothing on the task changed.\n", d.Name())
			} else {
				fmt.Fprintln(out, "\nDry run: nothing recorded (pass --record to write to the ledger). Nothing on the task changed.")
			}
			return nil
		}),
	}
	cmd.Flags().StringVar(&set, "set", "", "question set id")
	cmd.Flags().StringVar(&task, "task", "", "task id")
	cmd.Flags().StringVar(&decName, "decider", "baseline", "decider: baseline")
	cmd.Flags().BoolVar(&record, "record", false, "record the answers in the ledger")
	_ = cmd.MarkFlagRequired("set")
	_ = cmd.MarkFlagRequired("task")
	return cmd
}

func judgeLedgerCmd() *cobra.Command {
	var set, subject, disposition string
	var limit int
	cmd := &cobra.Command{
		Use:          "ledger",
		Short:        "List the enrichment ledger",
		SilenceUsage: true,
		RunE: withJudge(func(ctx context.Context, svc *service.Service, out io.Writer) error {
			rows, err := svc.Judge.Judgments(sqlstore.JudgmentFilter{SetID: set, SubjectID: subject, Disposition: disposition, Limit: limit})
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSUBJECT\tSET\tQUESTION\tCONF\tSUGGESTS\tDISPOSITION\tPROVIDER\tMODEL")
			for _, r := range rows {
				fmt.Fprintf(tw, "%d\t%s\t%s v%d\t%s\t%.2f\t%v\t%s\t%s\t%s\n", r.ID, r.SubjectID, r.SetID, r.SetVersion, r.QuestionKey, r.Confidence, r.Suggested, r.Disposition, r.Provider, r.Model)
			}
			return tw.Flush()
		}),
	}
	cmd.Flags().StringVar(&set, "set", "", "only this set")
	cmd.Flags().StringVar(&subject, "subject", "", "only this task or item id")
	cmd.Flags().StringVar(&disposition, "disposition", "", "suggested, accepted, rejected, auto_applied or superseded")
	cmd.Flags().IntVar(&limit, "limit", 50, "rows")
	return cmd
}
