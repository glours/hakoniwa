package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootHelp(t *testing.T) {
	root := newRootCmd()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	out := buf.String()
	for _, sub := range []string{"up", "down", "plan", "ps", "logs", "version"} {
		if !strings.Contains(out, sub) {
			t.Errorf("--help output missing subcommand %q", sub)
		}
	}
}

func TestVersionCmd(t *testing.T) {
	root := newRootCmd()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("version returned error: %v", err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "hako ") {
		t.Errorf("version output %q does not start with 'hako '", out)
	}
}

func TestLogsSubcmd(t *testing.T) {
	// Ensure logs subcommand can be invoked without a -f shorthand panic.
	root := newRootCmd()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"logs", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("logs --help returned error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "follow") {
		t.Errorf("logs --help output missing --follow flag description, got: %s", out)
	}
}

func TestSubcmdStubs(t *testing.T) {
	for _, sub := range []string{"up", "down", "plan", "ps", "logs"} {
		root := newRootCmd()
		buf := &bytes.Buffer{}
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{sub})
		err := root.Execute()
		if err == nil {
			t.Errorf("%s stub should return an error", sub)
		}
		if !strings.Contains(err.Error(), "not implemented") {
			t.Errorf("%s error %q does not mention 'not implemented'", sub, err)
		}
	}
}
