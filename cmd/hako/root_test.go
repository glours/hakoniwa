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

func TestGlobalFlags(t *testing.T) {
	root := newRootCmd()
	if root.PersistentFlags().Lookup("file") == nil {
		t.Error("missing global flag --file")
	}
	if root.PersistentFlags().Lookup("json") == nil {
		t.Error("missing global flag --json")
	}
}
