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

	errs := 0
	for _, problem := range problems {
		message := problem.Message
		if problem.Warning {
			message = "warning: " + message
		} else {
			errs++
		}
		if problem.Line > 0 {
			_, _ = fmt.Fprintf(out, "%s:%d: %s: %s\n", path, problem.Line, problem.Path, message)
			continue
		}
		_, _ = fmt.Fprintf(out, "%s: %s: %s\n", path, problem.Path, message)
	}
	if errs == 0 {
		// Warnings tell the operator what a profile grants; they do not fail
		// the lint.
		_, _ = fmt.Fprintf(out, "profiles OK with %d warning(s): %s\n", len(problems), path)
		return nil
	}
	return fmt.Errorf("profiles lint failed: %d problem(s)", errs)
}
