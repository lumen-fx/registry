package main

import (
	"os"

	"github.com/lumen-fx/registry/cli/cmd"
	"github.com/lumen-fx/registry/cli/internal"
)

func main() {
	// The exit code is part of the contract with the host CLIs that shell out
	// to lpm: 2 for a usage mistake, 3 for a lock that would change under
	// --locked, 4 for something --offline cannot reach.
	if err := cmd.Execute(); err != nil {
		os.Exit(internal.ExitCodeFor(err))
	}
}
