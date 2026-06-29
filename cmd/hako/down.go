package main

import "github.com/spf13/cobra"

func newDownCmd(file *string, jsonOutput *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop and remove all agents in the project",
		Args:  cobra.NoArgs,
		RunE:  notImplemented,
	}
}
