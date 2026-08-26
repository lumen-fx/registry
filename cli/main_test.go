package main

import (
	"os"
	"testing"
)

// The test binary's own flags would reach the root command through os.Args,
// so the test pins a bare invocation, which succeeds and returns here.
func TestMainRunsTheRootCommand(t *testing.T) {
	old := os.Args
	os.Args = []string{"lpm"}
	defer func() { os.Args = old }()
	main()
}
