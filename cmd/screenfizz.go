package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	screenfizzleadengine "github.com/nextlevelbuilder/goclaw/internal/screenfizz/leadengine"
)

func screenFizzCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "screenfizz",
		Short: "ScreenFizz tools",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "test-insert",
		Short: "Insert one test ScreenFizz business",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := screenfizzleadengine.ConfigFromEnv()
			if err != nil {
				return err
			}
			if err := screenfizzleadengine.InsertTestBusiness(cmd.Context(), cfg); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Successfully inserted test business.")
			return nil
		},
	})
	cmd.AddCommand(screenFizzLeadEngineCmd())
	cmd.AddCommand(&cobra.Command{
		Use:   "enrich",
		Short: "Download and save homepage HTML for ScreenFizz prospects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := screenfizzleadengine.ConfigFromEnv()
			if err != nil {
				return err
			}
			return screenfizzleadengine.EnrichProspects(cmd.Context(), cfg)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "parse",
		Short: "Parse saved ScreenFizz prospect homepage HTML",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := screenfizzleadengine.ConfigFromEnv()
			if err != nil {
				return err
			}
			return screenfizzleadengine.ParseProspects(cmd.Context(), cfg)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "analyse",
		Short: "Analyse parsed ScreenFizz prospects with AI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := screenfizzleadengine.ConfigFromEnv()
			if err != nil {
				return err
			}
			return screenfizzleadengine.AnalyseProspects(cmd.Context(), cfg)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "review",
		Short: "Review pending ScreenFizz email drafts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := screenfizzleadengine.ConfigFromEnv()
			if err != nil {
				return err
			}
			return screenfizzleadengine.ReviewProspects(cmd.Context(), cfg, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "send",
		Short: "Send approved ScreenFizz emails via Brevo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := screenfizzleadengine.ConfigFromEnv()
			if err != nil {
				return err
			}
			result, err := screenfizzleadengine.SendApprovedProspects(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Sent: %d\nFailed: %d\n", result.Sent, result.Failed)
			return err
		},
	})
	return cmd
}

func screenFizzLeadEngineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "leadengine",
		Short: "ScreenFizz Lead Engine",
	}
	var resumeRunID string
	testRunCmd := &cobra.Command{
		Use:   "test-run",
		Short: "Run the small ScreenFizz Apify import test",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runner, err := screenfizzleadengine.NewRunnerFromEnv()
			if err != nil {
				return err
			}
			var result screenfizzleadengine.RunResult
			if resumeRunID != "" {
				result, err = runner.ImportCompletedRun(cmd.Context(), resumeRunID)
			} else {
				result, err = runner.RunTest(cmd.Context())
			}
			if err != nil {
				return err
			}
			printScreenFizzImportSummary(cmd, result)
			return nil
		},
	}
	testRunCmd.Flags().StringVar(&resumeRunID, "resume-run-id", "", "completed Apify run ID to import without starting another run")
	cmd.AddCommand(testRunCmd)
	cmd.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "Discover ScreenFizz business leads from Google Places",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			runner, err := screenfizzleadengine.NewRunnerFromEnv()
			if err != nil {
				return err
			}
			result, err := runner.Run(cmd.Context())
			if err != nil {
				return err
			}
			printScreenFizzImportSummary(cmd, result)
			return nil
		},
	})
	return cmd
}

func printScreenFizzImportSummary(cmd *cobra.Command, result screenfizzleadengine.RunResult) {
	fmt.Fprintf(cmd.OutOrStdout(), "Found: %d\nInserted: %d\nSkipped (no website): %d\nSkipped (no email): %d\nSkipped (closed): %d\nSkipped (duplicate): %d\nProspects added: %d\nProspects skipped: %d\n",
		result.TotalReturned,
		result.Inserted,
		result.NoWebsiteSkipped,
		result.NoEmailSkipped,
		result.ClosedSkipped,
		result.DuplicatesSkipped,
		result.ProspectsAdded,
		result.ProspectsSkipped)
}
