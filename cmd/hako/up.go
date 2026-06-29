package main

import "github.com/spf13/cobra"

func newUpCmd(file *string, jsonOutput *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Create and start all agents defined in the project file",
		Args:  cobra.NoArgs,
		RunE:  notImplemented,
	}
}
