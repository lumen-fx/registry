package internal

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/lumen-fx/registry/cli/req"
)

// FromCommandLine labels a requirement the host CLI passed, as opposed to one
// that came from a package's own dependencies.
const FromCommandLine = "the command line"

// A resolution walks the dependency graph and settles one version per name,
// so it terminates once every name stops changing. The bound turns a bug that
// would loop forever into an error.
const maxRounds = 1000

// Requirement is one demand for a package: what is wanted, and who wants it.
// The requirer is what an unsatisfiable requirement gets reported with.
type Requirement struct {
	Name     string
	Spec     string
	Requirer string
}

// Source answers what a registry knows about a package. The registry client
// is one; a lock file read offline is another.
type Source interface {
	GetPackage(name string) (Package, error)
}

// Plan is everything a resolution needs that is not the registry itself.
type Plan struct {
	Target string
	// host name -> the version of that host, as passed with --host.
	Hosts map[string]string
	Roots []Requirement
	Lock  *Lock
	// Names whose pin is ignored, so `update` can move them while the rest
	// of the graph stays where it is. RefreshAll ignores every pin.
	Refresh    map[string]bool
	RefreshAll bool
}

// Choice is one settled package: the version, the artifact to install, and
// what it depends on.
type Choice struct {
	Name     string
	Version  string
	Platform string
	Artifact Artifact
	// The release's own requirements, kept so a later round can rebuild the
	// graph after some other package moves.
	Specs Requirements
	// Dependency name -> the version resolved for it. Filled once the whole
	// graph has settled.
	Dependencies map[string]string
}

// Resolve settles one version per package name: the highest release that
// satisfies every requirement on it, has an artifact for the target, and
// needs nothing of its hosts they cannot give. A pin from the lock wins over
// a higher release while it still satisfies everything.
func Resolve(source Source, plan Plan) ([]Choice, error) {
	hosts, err := parseHosts(plan.Hosts)
	if err != nil {
		return nil, err
	}

	chosen := map[string]Choice{}
	for round := 0; ; round++ {
		if round > maxRounds {
			return nil, fmt.Errorf("resolution did not settle after %d rounds", maxRounds)
		}

		required, reachable := walk(plan.Roots, chosen)

		// The first name that has no choice yet, or whose choice no longer
		// satisfies everything asked of it.
		unsettled := ""
		for _, name := range slices.Sorted(maps.Keys(required)) {
			choice, ok := chosen[name]
			if !ok || !satisfies(choice.Version, required[name]) {
				unsettled = name
				break
			}
		}
		if unsettled == "" {
			return finish(chosen, reachable), nil
		}

		choice, err := pick(source, plan, hosts, unsettled, required[unsettled])
		if err != nil {
			return nil, err
		}
		chosen[unsettled] = choice
	}
}

// walk rebuilds the requirement graph from the roots through what has been
// chosen so far, so a package that moved stops imposing its old dependencies.
func walk(roots []Requirement, chosen map[string]Choice) (map[string][]Requirement, map[string]bool) {
	required := map[string][]Requirement{}
	reachable := map[string]bool{}

	queue := make([]string, 0, len(roots))
	for _, root := range roots {
		required[root.Name] = append(required[root.Name], root)
		queue = append(queue, root.Name)
	}

	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if reachable[name] {
			continue
		}
		reachable[name] = true

		choice, ok := chosen[name]
		if !ok {
			continue
		}
		requirer := name + " " + choice.Version
		for _, dep := range slices.Sorted(maps.Keys(choice.Specs)) {
			required[dep] = append(required[dep], Requirement{
				Name: dep, Spec: choice.Specs[dep], Requirer: requirer,
			})
			queue = append(queue, dep)
		}
	}

	return required, reachable
}

// finish drops anything no longer reachable from the roots and fills in the
// version each dependency resolved to.
func finish(chosen map[string]Choice, reachable map[string]bool) []Choice {
	out := make([]Choice, 0, len(reachable))
	for _, name := range slices.Sorted(maps.Keys(reachable)) {
		choice, ok := chosen[name]
		if !ok {
			continue
		}
		choice.Dependencies = map[string]string{}
		for dep := range choice.Specs {
			if resolved, ok := chosen[dep]; ok {
				choice.Dependencies[dep] = resolved.Version
			}
		}
		out = append(out, choice)
	}
	return out
}

func parseHosts(hosts map[string]string) (map[string]*semver.Version, error) {
	parsed := make(map[string]*semver.Version, len(hosts))
	for name, raw := range hosts {
		version, err := semver.NewVersion(raw)
		if err != nil {
			return nil, fmt.Errorf("--host %s@%s: %q is not a version", name, raw, raw)
		}
		parsed[name] = version
	}
	return parsed, nil
}

func satisfies(version string, requirements []Requirement) bool {
	parsed, err := semver.StrictNewVersion(version)
	if err != nil {
		return false
	}
	for _, requirement := range requirements {
		constraint, err := req.Parse(requirement.Spec)
		if err != nil || !constraint.Check(parsed) {
			return false
		}
	}
	return true
}

// candidate is a release whose version parsed, kept with the parsed version
// so the list can be ordered without parsing again.
type candidate struct {
	release Release
	version *semver.Version
}

// pick chooses the version of one package, or explains why no version works.
func pick(source Source, plan Plan, hosts map[string]*semver.Version, name string, requirements []Requirement) (Choice, error) {
	constraints := make([]*semver.Constraints, len(requirements))
	for i, requirement := range requirements {
		constraint, err := req.Parse(requirement.Spec)
		if err != nil {
			return Choice{}, fmt.Errorf("%s requires %s %s: %w",
				requirement.Requirer, name, requirement.Spec, err)
		}
		constraints[i] = constraint
	}

	pkg, err := source.GetPackage(name)
	if err != nil {
		return Choice{}, fmt.Errorf("%s (required by %s): %w", name, requirers(requirements), err)
	}

	// Newest first, so the first candidate that clears every filter is the
	// one to take.
	var candidates []candidate
	for _, release := range pkg.Releases {
		version, err := semver.StrictNewVersion(release.Version)
		if err != nil {
			continue // a version lpm cannot order is a version it cannot pick
		}
		candidates = append(candidates, candidate{release: release, version: version})
	}
	slices.SortFunc(candidates, func(a, b candidate) int { return b.version.Compare(a.version) })

	matching := slices.DeleteFunc(slices.Clone(candidates), func(c candidate) bool {
		for _, constraint := range constraints {
			if !constraint.Check(c.version) {
				return true
			}
		}
		return false
	})
	if len(matching) == 0 {
		return Choice{}, fmt.Errorf("no version of %s satisfies %s%s",
			name, demands(requirements), published(candidates))
	}

	hosted := slices.DeleteFunc(slices.Clone(matching), func(c candidate) bool {
		return unmetHost(c.release, hosts) != ""
	})
	if len(hosted) == 0 {
		return Choice{}, fmt.Errorf("%s %s needs %s",
			name, matching[0].release.Version, unmetHost(matching[0].release, hosts))
	}

	usable := slices.DeleteFunc(slices.Clone(hosted), func(c candidate) bool {
		_, ok := c.release.Pick(plan.Target)
		return !ok
	})
	if len(usable) == 0 {
		return Choice{}, fmt.Errorf("%s %s publishes no artifact for %s or %s",
			name, hosted[0].release.Version, plan.Target, AnyTarget)
	}

	// A pin that still works keeps the graph still: an unrelated install
	// should not move a package nobody asked to move.
	best := usable[0]
	if pin, ok := plan.Lock.Find(name); ok && !plan.RefreshAll && !plan.Refresh[name] {
		for _, c := range usable {
			if c.release.Version == pin.Version {
				best = c
				break
			}
		}
	}

	artifact, _ := best.release.Pick(plan.Target)
	specs := best.release.Dependencies
	if specs == nil {
		specs = Requirements{}
	}
	return Choice{
		Name:     pkg.Name,
		Version:  best.release.Version,
		Platform: pkg.Platform,
		Artifact: artifact,
		Specs:    specs,
	}, nil
}

// unmetHost names the first host requirement this release does not get,
// including a host nobody passed. It returns "" when every one is met.
func unmetHost(release Release, hosts map[string]*semver.Version) string {
	for _, name := range slices.Sorted(maps.Keys(release.Requires)) {
		spec := release.Requires[name]
		constraint, err := req.Parse(spec)
		if err != nil {
			return fmt.Sprintf("%s %s, which is not a version requirement", name, spec)
		}
		version, ok := hosts[name]
		if !ok {
			return fmt.Sprintf("%s %s, and no --host %s was passed", name, spec, name)
		}
		if !constraint.Check(version) {
			return fmt.Sprintf("%s %s, and you have %s %s", name, spec, name, version)
		}
	}
	return ""
}

// demands lists who asked for what, so a conflict names both sides.
func demands(requirements []Requirement) string {
	parts := make([]string, len(requirements))
	for i, r := range requirements {
		parts[i] = fmt.Sprintf("%s (%s)", r.Requirer, r.Spec)
	}
	return strings.Join(parts, " and ")
}

func requirers(requirements []Requirement) string {
	parts := make([]string, len(requirements))
	for i, r := range requirements {
		parts[i] = r.Requirer
	}
	return strings.Join(parts, " and ")
}

func published(candidates []candidate) string {
	if len(candidates) == 0 {
		return "; it has published no releases"
	}
	versions := make([]string, len(candidates))
	for i, c := range candidates {
		versions[i] = c.release.Version
	}
	return "; published: " + strings.Join(versions, ", ")
}

// LockSource answers from the lock file alone, for `--offline`. A pin was
// already checked against its host requirements when it was written, so it
// carries none; its dependencies are pinned exactly.
type LockSource struct{ Lock *Lock }

func (s LockSource) GetPackage(name string) (Package, error) {
	pin, ok := s.Lock.Find(name)
	if !ok {
		return Package{}, Fail(ExitOffline, "it is not in the lock, and --offline reaches no registry")
	}

	artifacts := make([]Artifact, 0, len(pin.Artifacts))
	for _, target := range slices.Sorted(maps.Keys(pin.Artifacts)) {
		artifacts = append(artifacts, Artifact{
			Target: target,
			SHA256: strings.TrimPrefix(pin.Artifacts[target], "sha256:"),
		})
	}

	dependencies := Requirements{}
	for _, entry := range pin.Dependencies {
		depName, depVersion, found := strings.Cut(entry, " ")
		if found {
			dependencies[depName] = "=" + depVersion
		}
	}

	return Package{
		Name:     pin.Name,
		Platform: pin.Platform,
		Releases: []Release{{
			Version:      pin.Version,
			Dependencies: dependencies,
			Requires:     Requirements{},
			Artifacts:    artifacts,
		}},
	}, nil
}
