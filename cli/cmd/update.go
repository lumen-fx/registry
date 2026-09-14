package cmd

import (
	"github.com/spf13/cobra"
)

var updateOpts installOptions

var updateCmd = &cobra.Command{
	Use:   "update [name...]",
	Short: "Re-resolve packages, ignoring what the lock pins them to",
	Long: `Resolves the same way install does, except that the named packages
are free to move: their pins are ignored and the newest release satisfying
every requirement wins. Name nothing and every pin is up for reconsideration.

Everything the named packages do not reach stays where the lock put it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		refresh := make(map[string]bool, len(args))
		for _, name := range args {
			refresh[name] = true
		}
		return updateOpts.run(cmd, refresh, len(args) == 0)
	},
}

func init() {
	updateOpts.bind(updateCmd)
	rootCmd.AddCommand(updateCmd)
}
