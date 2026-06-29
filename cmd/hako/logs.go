package main

import "github.com/spf13/cobra"

func newLogsCmd(file *string, jsonOutput *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [agent]",
		Short: "Stream logs from an agent (or all agents)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  notImplemented,
	}
	cmd.Flags().BoolP("follow", "f", false, "follow log output")
	return cmd
}
