package main

import "github.com/spf13/cobra"

func newPsCmd(file *string, jsonOutput *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "ps",
		Short: "List running agents for the project",
		Args:  cobra.NoArgs,
		RunE:  notImplemented,
	}
}
