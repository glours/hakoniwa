package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/orchestrator"
	"github.com/glours/hakoniwa/internal/sandbox"
)

func newPlanCmd(file *string, _ *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "plan",
		Short: "Preview what hako up would do without making changes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPlan(cmd, *file)
		},
	}
}

func runPlan(cmd *cobra.Command, file string) error {
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

	orchestrator.WritePlanSummary(cmd.OutOrStdout(), lr.Project)
	if _, err := orch.Plan(cmd.Context(), lr.Project); err != nil {
		fmt.Fprintf(os.Stderr, "hako plan: %v\n", err)
		return err
	}
	return nil
}
