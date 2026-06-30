package orchestrator

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/glours/hakoniwa/internal/config"
)

// ANSI colour codes. When the output writer is not a terminal the codes are
// replaced with empty strings so output stays clean in pipes and log files.
const (
	ansiReset  = "\033[0m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiGray   = "\033[90m"
	ansiBold   = "\033[1m"
)

// colours bundles resolved ANSI codes (or empty strings) for one render pass.
type colours struct {
	create   string // green  — new resource
	converge string // yellow — resource needs changes
	reuse    string // gray   — no changes needed
	bold     string
	reset    string
}

// isTerminalWriter returns true when w is an *os.File whose fd is a terminal.
func isTerminalWriter(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func newColours(w io.Writer) colours {
	if !isTerminalWriter(w) {
		return colours{}
	}
	return colours{
		create:   ansiGreen,
		converge: ansiYellow,
		reuse:    ansiGray,
		bold:     ansiBold,
		reset:    ansiReset,
	}
}

// RenderPlan writes the rich plan output to w.
//
//	Project: debug-collab
//
//	  + reproducer  (claude/node)  → will create debug-collab-reproducer
//	      ports:    8080:8080
//	      secrets:  GH_TOKEN
//	      emits:    repro.ready
//
//	Plan: 2 to create, 0 to reuse, 0 to converge.
func RenderPlan(w io.Writer, projectName string, entries []PlanEntry) {
	c := newColours(w)

	logf(w, "\n%sProject:%s %s\n", c.bold, c.reset, projectName)

	for _, e := range entries {
		logf(w, "\n")
		renderPlanEntry(w, c, e)
	}

	logf(w, "\n")
	renderPlanSummary(w, c, entries)
	logf(w, "\n")
}

// renderPlanEntry renders one agent block.
func renderPlanEntry(w io.Writer, c colours, e PlanEntry) {
	sigil, colour, _ := planSigilVerb(c, e.Action)

	// Agent type string: "claude" or "claude/node" when template differs.
	agentType := e.AgentKind
	if e.Template != "" {
		agentType = e.AgentKind + "/" + e.Template
	}

	// Header line: "  + reproducer  (claude/node)  → will create debug-collab-reproducer"
	sandboxVerb := sandboxActionText(e.Action, e.Sandbox, e.CurrentStatus)
	logf(w, "  %s%s%s %-14s %s(%-20s)%s  %s\n",
		colour, sigil, c.reset,
		e.Agent,
		c.reuse, agentType, c.reset,
		sandboxVerb,
	)

	// Detail lines (indented, only when non-empty).
	if len(e.AllPorts) > 0 && e.Action != ActionConverge {
		logf(w, "      ports:    %s\n", strings.Join(e.AllPorts, ", "))
	}
	if len(e.AddPorts) > 0 {
		prefixed := make([]string, len(e.AddPorts))
		for i, p := range e.AddPorts {
			prefixed[i] = "+" + p
		}
		logf(w, "      ports:    %s%s%s\n", c.converge, strings.Join(prefixed, ", "), c.reset)
	}
	if len(e.SecretEnvs) > 0 {
		logf(w, "      secrets:  %s\n", strings.Join(e.SecretEnvs, ", "))
	}
	if len(e.Emits) > 0 {
		logf(w, "      emits:    %s\n", strings.Join(e.Emits, ", "))
	}
	if len(e.DependsOn) > 0 {
		depLines := renderDependsOn(e.DependsOn)
		for _, dl := range depLines {
			logf(w, "      depends:  %s\n", dl)
		}
	}
	if len(e.Reach) > 0 {
		logf(w, "      reach:    %s\n", strings.Join(e.Reach, ", "))
	}
	if len(e.Kits) > 0 {
		logf(w, "      kits:     %s\n", strings.Join(e.Kits, ", "))
	}
}

// planSigilVerb returns the sigil character, ANSI colour, and human verb
// for the given action.
func planSigilVerb(c colours, action AgentAction) (sigil, colour, verb string) {
	switch action {
	case ActionCreate:
		return "+", c.create, "create"
	case ActionConverge:
		return "~", c.converge, "converge"
	default: // ActionReuse
		return "=", c.reuse, "reuse"
	}
}

// sandboxActionText composes the "→ will create …" / "running (…)" tail.
func sandboxActionText(action AgentAction, sbxName, currentStatus string) string {
	switch action {
	case ActionCreate:
		return fmt.Sprintf("\u2192 will create %s", sbxName)
	case ActionConverge:
		if currentStatus != "" {
			return fmt.Sprintf("\u2192 will converge %s (%s)", sbxName, currentStatus)
		}
		return fmt.Sprintf("\u2192 will converge %s", sbxName)
	default: // reuse
		if currentStatus != "" {
			return fmt.Sprintf("%s (%s)", currentStatus, sbxName)
		}
		return sbxName
	}
}

// renderDependsOn formats depends_on entries into human-readable strings.
// on_event edges include the channel name; structural edges show the condition.
func renderDependsOn(deps map[string]config.DependsOnEntry) []string {
	if len(deps) == 0 {
		return nil
	}
	// Stable sort by dep name.
	names := make([]string, 0, len(deps))
	for n := range deps {
		names = append(names, n)
	}
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	out := make([]string, 0, len(deps))
	for _, n := range names {
		d := deps[n]
		if d.Condition == config.ConditionOnEvent && d.Channel != "" {
			out = append(out, fmt.Sprintf("%s on %s", n, d.Channel))
		} else {
			out = append(out, fmt.Sprintf("%s (%s)", n, d.Condition))
		}
	}
	return out
}

// renderPlanSummary writes the "Plan: N to create, N to reuse, N to converge." line.
func renderPlanSummary(w io.Writer, c colours, entries []PlanEntry) {
	var nCreate, nReuse, nConverge int
	for _, e := range entries {
		switch e.Action {
		case ActionCreate:
			nCreate++
		case ActionReuse:
			nReuse++
		case ActionConverge:
			nConverge++
		}
	}
	logf(w, "%sPlan:%s  %s%d to create%s,  %s%d to reuse%s,  %s%d to converge%s.\n",
		c.bold, c.reset,
		c.create, nCreate, c.reset,
		c.reuse, nReuse, c.reset,
		c.converge, nConverge, c.reset,
	)
}

// ---------------------------------------------------------------------------
// Ps renderer
// ---------------------------------------------------------------------------

// RenderPs writes the ps table to w with status-coloured output.
func RenderPs(w io.Writer, projectName string, entries []PsEntry) {
	c := newColours(w)

	if len(entries) == 0 {
		logf(w, "No sandboxes found for project %q.\n", projectName)
		return
	}

	// Header.
	logf(w, "\n%s%-16s  %-36s  %-10s  %s%s\n",
		c.bold, "AGENT", "SANDBOX", "STATUS", "PORTS", c.reset)
	logf(w, "%s\n", strings.Repeat("─", 80))

	for _, e := range entries {
		statusCol := statusColour(c, e.Status)
		ports := strings.Join(e.Ports, ", ")
		if ports == "" {
			ports = "—"
		}
		logf(w, "%-16s  %-36s  %s%-10s%s  %s\n",
			e.Agent, e.Name, statusCol, e.Status, c.reset, ports)
	}
	logf(w, "\n")
}

// statusColour returns the ANSI colour for a sandbox status.
func statusColour(c colours, status string) string {
	switch status {
	case "running":
		return c.create // green
	default:
		return c.reuse // gray
	}
}
