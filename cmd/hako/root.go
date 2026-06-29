package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags "-X main.version=<ver>".
// Falls back to VCS build info from the Go toolchain.
var version = ""

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return s.Value[:7]
			}
		}
	}
	return "dev"
}

func newRootCmd() *cobra.Command {
	var file string
	var jsonOutput bool

	root := &cobra.Command{
		Use:   "hako",
		Short: "Hakoniwa — a Compose-like orchestrator for Docker Sandboxes",
		Long: `hako reads a hakoniwa.yaml (or hako.yaml) file and orchestrates
multi-agent applications where each AI agent runs in its own Docker Sandbox.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVarP(&file, "file", "f", "",
		`project file (default: hakoniwa.yaml, hako.yaml, .sbxenv)`)
	root.PersistentFlags().BoolVar(&jsonOutput, "json", false,
		`output diagnostics as JSON`)

	root.AddCommand(
		newUpCmd(&file, &jsonOutput),
		newDownCmd(&file, &jsonOutput),
		newPlanCmd(&file, &jsonOutput),
		newPsCmd(&file, &jsonOutput),
		newLogsCmd(&file, &jsonOutput),
		newVersionCmd(),
	)

	return root
}

func notImplemented(cmd *cobra.Command, _ []string) error {
	return fmt.Errorf("%s: not implemented yet", cmd.Name())
}

// exitErr prints err to stderr and returns exit code 1. Used by commands that
// cannot propagate errors through cobra (e.g. PersistentPreRun).
func exitErr(err error) {
	fmt.Fprintf(os.Stderr, "hako: %v\n", err)
	os.Exit(1)
}
