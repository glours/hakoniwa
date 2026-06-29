package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/orchestrator"
	"github.com/glours/hakoniwa/internal/sandbox"
)

func newPsCmd(file *string, _ *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "ps",
		Short: "List running agents for the project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPs(cmd, *file)
		},
	}
}

func runPs(cmd *cobra.Command, file string) error {
	if file == "" {
		var err error
		file, err = config.FindProjectFile("")
		if err != nil {
			return err
		}
	}

	lr, err := config.Load(file)
	if err != nil {
		return err
	}

	client, err := sandbox.NewDaemonClient()
	if err != nil {
		return fmt.Errorf("connect to sandboxd: %w", err)
	}

	sbx := sandbox.NewSbxCLIAdapter(client)

	orch, err := orchestrator.NewOrchestrator(client, sbx, lr.Project.Name, cmd.OutOrStdout())
	if err != nil {
		return err
	}

	if _, err := orch.Ps(cmd.Context()); err != nil {
		fmt.Fprintf(os.Stderr, "hako ps: %v\n", err)
		return err
	}
	return nil
}
