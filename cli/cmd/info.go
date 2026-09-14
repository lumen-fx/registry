package cmd

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

var infoRegistry string

var infoCmd = &cobra.Command{
	Use:   "info <name>",
	Short: "Show one package, its releases, and what each needs",
	Args:  exactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := readOnlyClient(infoRegistry)
		if err != nil {
			return err
		}

		pkg, err := client.GetPackage(args[0])
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "%s (%s)\n", pkg.Name, pkg.Platform)
		if pkg.Description != "" {
			fmt.Fprintln(out, pkg.Description)
		}
		if pkg.Publisher != nil {
			fmt.Fprintf(out, "published by %s\n", pkg.Publisher.Username)
		}

		if len(pkg.Releases) == 0 {
			fmt.Fprintln(out, "\nNo releases published yet.")
			return nil
		}

		fmt.Fprintln(out, "\nReleases, newest first:")
		for _, release := range pkg.Releases {
			targets := make([]string, len(release.Artifacts))
			for i, artifact := range release.Artifacts {
				targets[i] = artifact.Target
			}
			fmt.Fprintf(out, "  %s  %s\n", release.Version, strings.Join(targets, " "))

			if line := requirementLine("depends on", release.Dependencies); line != "" {
				fmt.Fprintf(out, "    %s\n", line)
			}
			if line := requirementLine("needs", release.Requires); line != "" {
				fmt.Fprintf(out, "    %s\n", line)
			}
		}
		return nil
	},
}

func requirementLine(label string, requirements map[string]string) string {
	if len(requirements) == 0 {
		return ""
	}
	parts := make([]string, 0, len(requirements))
	for _, name := range slices.Sorted(maps.Keys(requirements)) {
		parts = append(parts, name+" "+requirements[name])
	}
	return label + " " + strings.Join(parts, ", ")
}

func init() {
	infoCmd.Flags().StringVar(&infoRegistry, "registry", "", "registry to read from")
	rootCmd.AddCommand(infoCmd)
}
