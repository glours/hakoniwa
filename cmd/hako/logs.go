package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/glours/hakoniwa/internal/config"
	"github.com/glours/hakoniwa/internal/sandbox"
	"github.com/glours/hakoniwa/internal/sandbox/sandboxapi"
)

func newLogsCmd(file *string, _ *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [agent]",
		Short: "Stream logs from an agent (or all agents)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			follow, _ := cmd.Flags().GetBool("follow")
			return runLogs(cmd, *file, args, follow)
		},
	}
	cmd.Flags().Bool("follow", false, "follow log output (block until session ends)")
	return cmd
}

func runLogs(cmd *cobra.Command, file string, args []string, follow bool) error {
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

	prefix := lr.Project.Name + "-"
	out := cmd.OutOrStdout()

	// Collect target agent names.
	var agentNames []string
	if len(args) == 1 {
		agentNames = []string{args[0]}
	} else {
		// All agents in declaration order (sorted).
		for name := range lr.Project.Agents {
			agentNames = append(agentNames, name)
		}
	}

	for _, name := range agentNames {
		sbxName := prefix + name

		// Verify the sandbox exists before attaching.
		info, err := client.InspectSandbox(cmd.Context(), sbxName)
		if err != nil {
			if sandbox.IsNotFound(err) {
				fmt.Fprintf(out, "[%s] sandbox %s not found — skipping\n", name, sbxName)
				continue
			}
			return fmt.Errorf("[%s] inspect: %w", name, err)
		}
		if info.Status != sandboxapi.SandboxInfoStatusRunning {
			fmt.Fprintf(out, "[%s] sandbox %s is %s — skipping\n", name, sbxName, info.Status)
			continue
		}

		// Attach a session to stream output.
		session, err := client.AttachAgentSession(cmd.Context(), sbxName, sandboxapi.AgentSessionRequest{})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] attach session: %v\n", name, err)
			continue
		}

		if follow {
			// Blocking: stream until session ends.
			if err := session.Stream(
				prefixWriter(out, "["+name+"] "),
				prefixWriter(os.Stderr, "["+name+"][err] "),
			); err != nil {
				fmt.Fprintf(os.Stderr, "[%s] stream error: %v\n", name, err)
			}
		} else {
			// Non-follow: drain available output then return.
			// We still call Stream which returns on EOF.
			if err := session.Stream(
				prefixWriter(out, "["+name+"] "),
				prefixWriter(os.Stderr, "["+name+"][err] "),
			); err != nil {
				fmt.Fprintf(os.Stderr, "[%s] stream error: %v\n", name, err)
			}
		}
		_ = session.Close()
	}
	return nil
}

// prefixWriter wraps w so every Write call prepends prefix to the output.
// It is a lightweight byte-level wrapper — suitable for prefixing log lines
// when the underlying stream is already line-buffered (stdcopy frames).
func prefixWriter(w io.Writer, prefix string) io.Writer {
	return &prefixedWriter{w: w, prefix: []byte(prefix)}
}

type prefixedWriter struct {
	w      io.Writer
	prefix []byte
}

func (p *prefixedWriter) Write(b []byte) (int, error) {
	// Prepend the prefix to each write.
	buf := make([]byte, 0, len(p.prefix)+len(b))
	buf = append(buf, p.prefix...)
	buf = append(buf, b...)
	if _, err := p.w.Write(buf); err != nil {
		return 0, err
	}
	return len(b), nil
}
