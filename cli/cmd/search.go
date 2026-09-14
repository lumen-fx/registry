package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/lumen-fx/registry/cli/internal"
	"github.com/spf13/cobra"
)

var searchPlatform, searchRegistry string

// readOnlyClient talks to the registry's public endpoints. It carries the
// saved token when there is one, and works without it.
func readOnlyClient(flag string) (*internal.Client, error) {
	registry, err := internal.ResolveRegistry(flag)
	if err != nil {
		return nil, err
	}
	config, err := internal.LoadConfig()
	if err != nil {
		return nil, err
	}
	return internal.NewClient(internal.Config{Registry: registry, Token: config.Token}), nil
}

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Find packages by name or description",
	Args:  exactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := readOnlyClient(searchRegistry)
		if err != nil {
			return err
		}

		packages, err := client.SearchPackages(internal.PackageFilter{
			Search:   args[0],
			Platform: searchPlatform,
		})
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		if len(packages) == 0 {
			fmt.Fprintf(out, "Nothing matches %q\n", args[0])
			return nil
		}

		table := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, pkg := range packages {
			newest := "no releases"
			if len(pkg.Releases) > 0 {
				newest = pkg.Releases[0].Version
			}
			fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", pkg.Name, pkg.Platform, newest, pkg.Description)
		}
		return table.Flush()
	},
}

func init() {
	searchCmd.Flags().StringVarP(&searchPlatform, "platform", "p", "", "restrict to one platform, lumen or candela")
	searchCmd.Flags().StringVar(&searchRegistry, "registry", "", "registry to search")
	rootCmd.AddCommand(searchCmd)
}
