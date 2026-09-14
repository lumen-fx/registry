package cmd

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lumen-fx/registry/cli/internal"
	"github.com/spf13/cobra"
)

// usage marks a failure the caller can fix by invoking lpm differently.
func usage(format string, args ...any) error {
	return internal.Fail(internal.ExitUsage, format, args...)
}

// installOptions is the flag set install and update share. They resolve the
// same way; update differs only in which pins it is willing to move.
type installOptions struct {
	lock     string
	target   string
	registry string
	hosts    []string
	reqs     []string
	locked   bool
	offline  bool
	asJSON   bool
}

func (o *installOptions) bind(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVar(&o.lock, "lock", "", "path to the lock file lpm reads and writes")
	flags.StringVar(&o.target, "target", "", "machine to install for, e.g. "+internal.HostTarget())
	flags.StringVar(&o.registry, "registry", "", "registry to resolve against")
	flags.StringArrayVar(&o.hosts, "host", nil, "a host and its version, as NAME@VERSION; repeatable")
	flags.StringArrayVar(&o.reqs, "req", nil, "a package and what is wanted of it, as NAME@REQUIREMENT; repeatable")
	flags.BoolVar(&o.locked, "locked", false, "fail instead of writing a changed lock file")
	flags.BoolVar(&o.offline, "offline", false, "resolve from the lock and install from the cache, reaching no network")
	flags.BoolVar(&o.asJSON, "json", false, "report what was installed as JSON")
}

// pair splits NAME<sep>VALUE, the shape every repeatable flag here uses.
func pair(flag, raw, sep string) (string, string, error) {
	name, value, found := strings.Cut(raw, sep)
	name, value = strings.TrimSpace(name), strings.TrimSpace(value)
	if !found || name == "" || value == "" {
		return "", "", usage("--%s %s: write it as NAME%sVALUE", flag, raw, sep)
	}
	return name, value, nil
}

// run resolves, installs, and writes the lock. refresh names the pins it may
// move; refreshAll ignores every pin.
func (o *installOptions) run(cmd *cobra.Command, refresh map[string]bool, refreshAll bool) error {
	if o.lock == "" {
		return usage("--lock is required; it is the path lpm reads the pins from and writes them to")
	}
	if err := internal.CheckTarget(o.target); err != nil {
		return usage("%s", err)
	}
	if o.offline && o.registry != "" {
		return usage("--offline reaches no registry, so --registry has nothing to do")
	}

	lockPath, err := filepath.Abs(o.lock)
	if err != nil {
		return err
	}

	hosts := map[string]string{}
	for _, raw := range o.hosts {
		name, version, err := pair("host", raw, "@")
		if err != nil {
			return err
		}
		hosts[name] = version
	}

	var roots []internal.Requirement
	for _, raw := range o.reqs {
		name, spec, err := pair("req", raw, "@")
		if err != nil {
			return err
		}
		roots = append(roots, internal.Requirement{
			Name: name, Spec: spec, Requirer: internal.FromCommandLine,
		})
	}

	lock, err := internal.ReadLock(lockPath)
	if err != nil {
		return err
	}

	registry, err := internal.ResolveRegistry(o.registry)
	if err != nil {
		return err
	}

	plan := internal.Plan{
		Target:     o.target,
		Hosts:      hosts,
		Roots:      roots,
		Lock:       lock,
		Refresh:    refresh,
		RefreshAll: refreshAll,
	}

	var source internal.Source
	if o.offline {
		source = internal.LockSource{Lock: lock}
	} else {
		config, err := internal.LoadConfig()
		if err != nil {
			return err
		}
		source = internal.NewClient(internal.Config{Registry: registry, Token: config.Token})
	}

	choices, err := internal.Resolve(source, plan)
	if err != nil {
		// Offline saw only what the lock holds, so a requirement it could not
		// meet is something it was not allowed to look for, not something the
		// registry does not publish.
		if o.offline && internal.ExitCodeFor(err) == internal.ExitError {
			return internal.Fail(internal.ExitOffline, "%s; --offline resolved from the lock alone", err)
		}
		return err
	}

	// The lock is compared before anything is downloaded, so --locked fails
	// on a stale lock without touching the network or the cache.
	updated := internal.LockFor(lock, registry, choices)
	changed := !updated.Equal(lock)
	if changed && o.locked {
		return internal.Fail(internal.ExitLockChange,
			"%s is out of date and --locked forbids writing it", lockPath)
	}

	cache, err := internal.OpenCache()
	if err != nil {
		return err
	}
	fetcher, err := internal.NewFetcher()
	if err != nil {
		return err
	}

	installed, err := internal.Install(cache, fetcher, o.offline, choices)
	if err != nil {
		return err
	}

	if changed {
		if err := internal.WriteLock(lockPath, updated); err != nil {
			return err
		}
	}

	return o.report(cmd, lockPath, installed)
}

func (o *installOptions) report(cmd *cobra.Command, lockPath string, installed []internal.InstalledPackage) error {
	out := cmd.OutOrStdout()

	if o.asJSON {
		if installed == nil {
			installed = []internal.InstalledPackage{}
		}
		encoder := json.NewEncoder(out)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(internal.Report{
			Schema:   internal.ReportSchema,
			Lock:     lockPath,
			Packages: installed,
		})
	}

	for _, pkg := range installed {
		fmt.Fprintf(out, "%s %s (%s) %s\n", pkg.Name, pkg.Version, pkg.Target, pkg.Dir)
	}
	return nil
}

var installOpts installOptions

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Resolve requirements, install the packages, and write the lock",
	Long: `Resolves every requirement to one version per package, downloads the
artifact for the target, verifies it against the digest the registry
published, and unpacks it into the cache.

Requirements come from the flags, never from a project file: the host CLI
reads its own manifest and passes what it found.

Versions already pinned in the lock are kept while they still satisfy every
requirement, so an install adds what is missing without moving what is not.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return installOpts.run(cmd, nil, false)
	},
}

func init() {
	installOpts.bind(installCmd)
	rootCmd.AddCommand(installCmd)
}
