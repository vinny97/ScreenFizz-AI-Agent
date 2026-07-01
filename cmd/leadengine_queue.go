package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nextlevelbuilder/goclaw/internal/leadengine"
)

func leadEngineQueueCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "queue",
		Short: "Queue READY_TO_EMAIL leads in Supabase",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := leadengine.NewFromEnv()
			if err != nil {
				return err
			}
			result, err := client.QueueReadyLeads(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Queued: %d\n", result.Queued)
			return nil
		},
	}
}
