// Package main is the entry point for the hako CLI.
package main

import (
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
