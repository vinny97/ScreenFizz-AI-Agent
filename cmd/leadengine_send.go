package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nextlevelbuilder/goclaw/internal/leadengine"
)

func leadEngineSendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "send",
		Short: "Send up to 100 EMAIL_READY leads via Brevo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := leadengine.NewFromEnv()
			if err != nil {
				return err
			}
			sender, err := leadengine.NewTestSenderFromEnv()
			if err != nil {
				return err
			}
			result, err := client.SendReadyLeads(cmd.Context(), sender)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Sent: %d\nFailed: %d\n", result.Sent, result.Failed)
			return nil
		},
	}
}
