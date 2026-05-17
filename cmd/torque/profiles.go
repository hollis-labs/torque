package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/spf13/cobra"
)

func newProfilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "profiles",
		Short: "Inspect and validate execution-template profiles",
	}
	cmd.AddCommand(newProfilesLintCmd())
	return cmd
}

func newProfilesLintCmd() *cobra.Command {
	var explicitPath string

	cmd := &cobra.Command{
		Use:          "lint",
		Short:        "Lint profiles.yaml against the current executor/provider catalog",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := lintProfilesPath(explicitPath)
			if err != nil {
				return err
			}
			return runProfilesLint(cmd.OutOrStdout(), path)
		},
	}
	cmd.Flags().StringVar(&explicitPath, "path", "", "Path to profiles.yaml (defaults to TORQUE_PROFILES_PATH or <data dir>/profiles.yaml)")
	return cmd
}

func lintProfilesPath(explicitPath string) (string, error) {
	if strings.TrimSpace(explicitPath) != "" {
		return explicitPath, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	path, err := resolveProfilesPath(cfg)
	if err != nil {
		return "", fmt.Errorf("resolve profiles path: %w", err)
	}
	return path, nil
}

func runProfilesLint(out io.Writer, path string) error {
	problems, err := config.LintProfilesFile(path)
	if err != nil {
		return err
	}
	if len(problems) == 0 {
		_, _ = fmt.Fprintf(out, "profiles OK: %s\n", path)
		return nil
	}

	for _, problem := range problems {
		if problem.Line > 0 {
			_, _ = fmt.Fprintf(out, "%s:%d: %s: %s\n", path, problem.Line, problem.Path, problem.Message)
			continue
		}
		_, _ = fmt.Fprintf(out, "%s: %s: %s\n", path, problem.Path, problem.Message)
	}
	return fmt.Errorf("profiles lint failed: %d problem(s)", len(problems))
}
