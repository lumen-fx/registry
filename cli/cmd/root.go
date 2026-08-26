package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Stamped by the linker at release time; see .goreleaser.yaml. They live in
// this package because rootCmd is what reports them, and -X can only reach a
// variable in the package that declares it.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:     "lpm",
	Short:   "lpm: the Lumen and Candela package manager",
	Long:    `lpm is the first-party package manager for Lumen and Candela`,
	Version: fmt.Sprintf("%s (%s, %s)", version, commit, date),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Please provide a command or use --help to learn more help")
	},
}

// Cobra already reports the error on stderr, so the caller only decides the
// exit code.
func Execute() error {
	return rootCmd.Execute()
}
