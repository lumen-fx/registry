package cmd

import (
	"fmt"

	"github.com/lumen-fx/registry/cli/internal"
	"github.com/spf13/cobra"
)

// publishClient loads the saved credentials or explains how to get them.
func publishClient() (*internal.Client, error) {
	cfg, err := internal.LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("not signed in; run `lpm login` first")
	}
	return internal.NewClient(cfg), nil
}

var publishPlatform, publishDescription string

var publishCmd = &cobra.Command{
	Use:   "publish <name>",
	Short: "Create a package you own on the registry",
	Long: `Creates a package under your account. Versions are added to it
afterwards with the release command.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := publishClient()
		if err != nil {
			return err
		}

		pkg, err := client.CreatePackage(internal.NewPackage{
			Platform:    publishPlatform,
			Name:        args[0],
			Description: publishDescription,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Published %s (%s)\n", pkg.Name, pkg.Platform)
		return nil
	},
}

var releaseURL, releaseDescription string

var releaseCmd = &cobra.Command{
	Use:   "release <package> <version>",
	Short: "Publish a release of your package",
	Long: `Publishes one version of a package you own. The URL is where clients
download the artifact from; it must be https.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := publishClient()
		if err != nil {
			return err
		}

		rel, err := client.CreateRelease(args[0], internal.NewRelease{
			URL:         releaseURL,
			Version:     args[1],
			Description: releaseDescription,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Released %s %s\n", args[0], rel.Version)
		return nil
	},
}

func init() {
	publishCmd.Flags().StringVarP(&publishPlatform, "platform", "p", "", "platform the package targets, e.g. lumen or candela")
	publishCmd.Flags().StringVarP(&publishDescription, "description", "d", "", "what the package is")
	_ = publishCmd.MarkFlagRequired("platform")

	releaseCmd.Flags().StringVarP(&releaseURL, "url", "u", "", "https URL of the release artifact")
	releaseCmd.Flags().StringVarP(&releaseDescription, "description", "d", "", "what changed in this release")
	_ = releaseCmd.MarkFlagRequired("url")

	rootCmd.AddCommand(publishCmd, releaseCmd)
}
