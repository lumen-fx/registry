package main

import (
	"os"

	"github.com/lumen-fx/registry/cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
