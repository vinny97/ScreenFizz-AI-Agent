package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/nextlevelbuilder/goclaw/internal/leadengine"
)

func leadEngineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "leadengine",
		Short: "Verify the Lead Engine Supabase connection",
	}
	cmd.AddCommand(leadEngineQualifyCmd())
	cmd.AddCommand(leadEngineQueueCmd())
	cmd.AddCommand(leadEngineGenerateCmd())
	cmd.AddCommand(leadEngineSendCmd())
	cmd.AddCommand(leadEngineSendTestCmd())
	cmd.AddCommand(&cobra.Command{
		Use:   "campaigns",
		Short: "Return all Supabase campaigns as JSON",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := leadengine.NewFromEnv()
			if err != nil {
				return err
			}
			campaigns, err := client.ListCampaigns(cmd.Context())
			if err != nil {
				return err
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(campaigns); err != nil {
				return fmt.Errorf("encode campaigns: %w", err)
			}
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "Run the active campaign on Apify and print its dataset",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := leadengine.NewFromEnv()
			if err != nil {
				return err
			}
			campaign, err := client.GetActiveCampaign(cmd.Context())
			if err != nil {
				return err
			}
			apify, err := leadengine.NewApifyClientFromEnv()
			if err != nil {
				return err
			}
			items, err := apify.Run(cmd.Context(), campaign)
			if err != nil {
				return err
			}
			var output bytes.Buffer
			if err := json.Indent(&output, items, "", "  "); err != nil {
				return fmt.Errorf("format Apify dataset: %w", err)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), output.String())
			return err
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "import-file <json-file>",
		Short: "Import leads from a local JSON file into Supabase",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			items, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read leads file: %w", err)
			}
			client, err := leadengine.NewFromEnv()
			if err != nil {
				return err
			}
			campaignName, err := client.GetActiveCampaignName(cmd.Context())
			if err != nil {
				return err
			}
			result, err := client.ImportLeads(cmd.Context(), campaignName, items)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Imported: %d\nSkipped: %d\n", result.Imported, result.Skipped)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "run-now",
		Short: "Run the scheduled Lead Engine workflow immediately",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			job, err := leadengine.NewJobFromEnv(filepath.Join("data", "leads"))
			if err != nil {
				return err
			}
			result, err := job.Run(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Saved: %s\nImported: %d\nSkipped: %d\n",
				result.FilePath, result.Import.Imported, result.Import.Skipped)
			return nil
		},
	})
	return cmd
}
