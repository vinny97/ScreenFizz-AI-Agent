package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nextlevelbuilder/goclaw/internal/leadengine"
)

func leadEngineSendTestCmd() *cobra.Command {
	var to string

	cmd := &cobra.Command{
		Use:   "send-test",
		Short: "Send the first EMAIL_READY lead to a test address via Brevo",
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
			if err := client.SendTestEmail(cmd.Context(), sender, to); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Test email sent successfully.")
			return err
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "Email address to receive the test email")
	return cmd
}
