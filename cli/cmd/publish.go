package cmd

import (
	"fmt"
	"strings"

	"github.com/lumen-fx/registry/cli/internal"
	"github.com/lumen-fx/registry/cli/req"
	"github.com/spf13/cobra"
)

// authedClient loads the credentials every command that needs a token runs
// on, or explains how to get them. The registry comes from the same places
// the read-only commands take it: the flag, then the saved one, then
// LPM_REGISTRY, so a job that publishes to a staging registry names it once.
func authedClient(flag string) (*internal.Client, error) {
	registry, err := internal.ResolveRegistry(flag)
	if err != nil {
		return nil, err
	}
	cfg, err := internal.LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("not signed in; run `lpm login` first, or set LPM_TOKEN")
	}
	return internal.NewClient(internal.Config{Registry: registry, Token: cfg.Token}), nil
}

var publishPlatform, publishDescription, publishRegistry string

var publishCmd = &cobra.Command{
	Use:   "publish <name>",
	Short: "Create a package you own on the registry",
	Long: `Creates a package under your account. Versions are added to it
afterwards with the release command.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := authedClient(publishRegistry)
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

var releaseDescription, releaseRegistry string
var releaseArtifacts, releaseDeps, releaseRequires []string

var releaseCmd = &cobra.Command{
	Use:   "release <package> <version>",
	Short: "Publish a release of your package",
	Long: `Publishes one version of a package you own.

You host the archives and the registry records where they are. Name one
--artifact per target you built for, and use the target "any" for an archive
that runs everywhere. Each URL must be https.

lpm downloads every artifact, hashes it, and publishes the digest and the size
alongside the URL, so what the registry records is what the URL served.`,
	Args: exactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := authedClient(releaseRegistry)
		if err != nil {
			return err
		}

		if len(releaseArtifacts) == 0 {
			return usage("--artifact TARGET=URL is required; a release nobody can download is not a release")
		}

		dependencies, err := requirementFlag("dep", releaseDeps)
		if err != nil {
			return err
		}
		requires, err := requirementFlag("requires", releaseRequires)
		if err != nil {
			return err
		}

		fetcher, err := internal.NewFetcher()
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		artifacts := make([]internal.Artifact, 0, len(releaseArtifacts))
		seen := map[string]bool{}
		for _, raw := range releaseArtifacts {
			target, location, err := pair("artifact", raw, "=")
			if err != nil {
				return err
			}
			if !internal.ValidTarget(target) {
				return usage("--artifact %s: unknown target %q; use one of %s",
					raw, target, strings.Join(internal.Targets, ", "))
			}
			if seen[target] {
				return usage("--artifact %s: %s is already named by another artifact", raw, target)
			}
			seen[target] = true

			fmt.Fprintf(out, "Hashing %s\n", location)
			sum, size, err := fetcher.Measure(location)
			if err != nil {
				return err
			}
			artifacts = append(artifacts, internal.Artifact{
				Target: target, URL: location, SHA256: sum, Size: size,
			})
		}

		rel, err := client.CreateRelease(args[0], internal.NewRelease{
			Version:      args[1],
			Description:  releaseDescription,
			Dependencies: dependencies,
			Requires:     requires,
			Artifacts:    artifacts,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Released %s %s with %d artifact(s)\n", args[0], rel.Version, len(rel.Artifacts))
		return nil
	},
}

// requirementFlag turns repeated NAME@REQUIREMENT flags into the map the
// registry stores. A requirement lpm cannot parse is rejected here, rather
// than published for every client to fail on.
func requirementFlag(flag string, values []string) (internal.Requirements, error) {
	requirements := internal.Requirements{}
	for _, raw := range values {
		name, spec, err := pair(flag, raw, "@")
		if err != nil {
			return nil, err
		}
		if _, dup := requirements[name]; dup {
			return nil, usage("--%s %s: %s is already named", flag, raw, name)
		}
		if _, err := req.Parse(spec); err != nil {
			return nil, usage("--%s %s: %s", flag, raw, err)
		}
		requirements[name] = spec
	}
	return requirements, nil
}

func init() {
	publishCmd.Flags().StringVarP(&publishPlatform, "platform", "p", "", "platform the package targets, lumen or candela")
	publishCmd.Flags().StringVarP(&publishDescription, "description", "d", "", "what the package is")
	publishCmd.Flags().StringVar(&publishRegistry, "registry", "", "registry to publish to")
	_ = publishCmd.MarkFlagRequired("platform")

	releaseCmd.Flags().StringArrayVar(&releaseArtifacts, "artifact", nil, "an archive to publish, as TARGET=URL; repeatable")
	releaseCmd.Flags().StringArrayVar(&releaseDeps, "dep", nil, "a package this release needs, as NAME@REQUIREMENT; repeatable")
	releaseCmd.Flags().StringArrayVar(&releaseRequires, "requires", nil, "a host this release needs, as NAME@REQUIREMENT; repeatable")
	releaseCmd.Flags().StringVarP(&releaseDescription, "description", "d", "", "what changed in this release")
	releaseCmd.Flags().StringVar(&releaseRegistry, "registry", "", "registry to release to")

	rootCmd.AddCommand(publishCmd, releaseCmd)
}
