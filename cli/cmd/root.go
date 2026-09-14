package cmd

import (
	"fmt"

	"github.com/lumen-fx/registry/cli/internal"
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
	// A failure is one line on stderr, written by Execute. Cobra's own report
	// prints the usage screen after it, which buries the line a host CLI is
	// trying to relay.
	SilenceErrors: true,
	SilenceUsage:  true,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Please provide a command or use --help to learn more help")
	},
}

// exactArgs counts positional arguments and reports a miscount as a usage
// error, so the caller exits with the usage code rather than the general one.
func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return internal.Fail(internal.ExitUsage,
				"%s takes %d argument(s), and got %d", cmd.Name(), n, len(args))
		}
		return nil
	}
}

// Execute runs the command and reports a failure as one line on stderr. The
// caller turns the error into an exit code.
func Execute() error {
	err := rootCmd.Execute()
	if err != nil {
		fmt.Fprintln(rootCmd.ErrOrStderr(), "lpm: "+err.Error())
	}
	return err
}

func init() {
	// An unknown flag or a malformed value is the caller's mistake, not the
	// registry's, and it exits with the usage code.
	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return internal.Fail(internal.ExitUsage, "%s", err)
	})
}
