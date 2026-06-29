package main

import "github.com/spf13/cobra"

func newLogsCmd(file *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [agent]",
		Short: "Stream logs from an agent (or all agents)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  notImplemented,
	}
	cmd.Flags().Bool("follow", false, "follow log output")
	return cmd
}
