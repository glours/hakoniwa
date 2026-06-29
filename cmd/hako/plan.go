package main

import "github.com/spf13/cobra"

func newPlanCmd(file *string, jsonOutput *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "plan",
		Short: "Preview what hako up would do without making changes",
		Args:  cobra.NoArgs,
		RunE:  notImplemented,
	}
}
