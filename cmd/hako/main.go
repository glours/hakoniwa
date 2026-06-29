// Package main is the entry point for the hako CLI.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "hako: %v\n", err)
		os.Exit(1)
	}
}
