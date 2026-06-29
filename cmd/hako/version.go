package main

import "github.com/spf13/cobra"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the hako version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Printf("hako %s\n", buildVersion())
			return nil
		},
	}
}
