package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nextlevelbuilder/goclaw/internal/leadengine"
)

func leadEngineQualifyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "qualify",
		Short: "Qualify NEW leads in Supabase",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := leadengine.NewFromEnv()
			if err != nil {
				return err
			}
			result, err := client.QualifyNewLeads(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Qualified: %d\nRejected: %d\n", result.Qualified, result.Rejected)
			return nil
		},
	}
}
