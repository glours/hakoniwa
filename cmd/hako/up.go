package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/orchestrator"
	"github.com/glours/hakoniwa/internal/sandbox"
)

func newUpCmd(file *string, _ *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Create and start all agents defined in the project file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUp(cmd, *file)
		},
	}
}

func runUp(cmd *cobra.Command, file string) error {
	// Resolve project file.
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

	if verr := config.Validate(lr); verr != nil {
		return verr
	}

	// Connect to sandboxd.
	client, err := sandbox.NewDaemonClient()
	if err != nil {
		return fmt.Errorf("connect to sandboxd: %w", err)
	}

	sbx := sandbox.NewSbxCLIAdapter(client)

	orch, err := orchestrator.NewOrchestrator(client, sbx, lr.Project.Name, cmd.OutOrStdout())
	if err != nil {
		return err
	}

	if err := orch.Up(cmd.Context(), lr.Project); err != nil {
		fmt.Fprintf(os.Stderr, "hako up: %v\n", err)
		return err
	}
	return nil
}
