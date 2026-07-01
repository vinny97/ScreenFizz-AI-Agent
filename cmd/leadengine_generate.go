package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nextlevelbuilder/goclaw/internal/leadengine"
)

func leadEngineGenerateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "generate",
		Short: "Generate email content for QUEUED leads in Supabase",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := leadengine.NewFromEnv()
			if err != nil {
				return err
			}
			result, err := client.GenerateQueuedEmails(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Generated: %d\n", result.Generated)
			return nil
		},
	}
}
