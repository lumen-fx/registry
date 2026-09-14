package internal

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeSource stands in for a registry: a fixed set of packages, and a count
// of how often each was asked for.
type fakeSource struct {
	packages map[string]Package
	asked    map[string]int
}

func (s *fakeSource) GetPackage(name string) (Package, error) {
	if s.asked == nil {
		s.asked = map[string]int{}
	}
	s.asked[name]++

	pkg, ok := s.packages[name]
	if !ok {
		return Package{}, errors.New("package doesn't exist")
	}
	return pkg, nil
}

// release builds one release with an `any` artifact, so the common case in a
// test reads as one line.
func release(version string, dependencies, requires Requirements) Release {
	return Release{
		Version:      version,
		Dependencies: dependencies,
		Requires:     requires,
		Artifacts: []Artifact{{
			Target: AnyTarget,
			URL:    "https://example.test/" + version + ".tar.gz",
			SHA256: fmt.Sprintf("%064x", len(version)),
			Size:   32,
		}},
	}
}

func pkg(name string, releases ...Release) Package {
	return Package{Name: name, Platform: "lumen", Releases: releases}
}

func root(name, spec string) Requirement {
	return Requirement{Name: name, Spec: spec, Requirer: FromCommandLine}
}

func byName(choices []Choice) map[string]Choice {
	out := make(map[string]Choice, len(choices))
	for _, c := range choices {
		out[c.Name] = c
	}
	return out
}

// The requirement grammar itself lives in the req module and is tested
// there. What matters here is that satisfies applies every requirement on a
// package, not just the first.
func TestSatisfiesAppliesEveryRequirement(t *testing.T) {
	both := []Requirement{root("geom", "^0.3"), root("geom", ">=0.3.5")}

	if satisfies("0.3.1", both) {
		t.Error("0.3.1 satisfied a requirement that wants at least 0.3.5")
	}
	if !satisfies("0.3.9", both) {
		t.Error("0.3.9 should satisfy both requirements")
	}
	if satisfies("0.4.0", both) {
		t.Error("0.4.0 satisfied a caret requirement on 0.3")
	}
}

func TestSatisfiesRejectsAVersionItCannotOrder(t *testing.T) {
	if satisfies("1.2", []Requirement{root("x", "*")}) {
		t.Error("satisfies accepted a version that is not semver")
	}
	if satisfies("1.2.3", []Requirement{root("x", "not a version")}) {
		t.Error("satisfies accepted a requirement it cannot parse")
	}
}

func TestResolveTakesTheHighestSatisfyingRelease(t *testing.T) {
	source := &fakeSource{packages: map[string]Package{
		"shape-tools": pkg("shape-tools",
			release("2.0.0", nil, nil),
			release("1.9.0", nil, nil),
			release("1.2.3", nil, nil),
		),
	}}

	choices, err := Resolve(source, Plan{
		Target: "linux-x86_64",
		Roots:  []Requirement{root("shape-tools", "^1.2")},
		Lock:   &Lock{Version: LockVersion},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(choices) != 1 || choices[0].Version != "1.9.0" {
		t.Fatalf("choices = %+v, want shape-tools 1.9.0", choices)
	}
	if choices[0].Platform != "lumen" || choices[0].Artifact.Target != AnyTarget {
		t.Errorf("choice = %+v, want the platform and the any artifact", choices[0])
	}
}

func TestResolveFollowsDependencies(t *testing.T) {
	source := &fakeSource{packages: map[string]Package{
		"shape-tools": pkg("shape-tools", release("1.2.3", Requirements{"geom": "^0.3"}, nil)),
		"geom":        pkg("geom", release("0.4.0", nil, nil), release("0.3.1", nil, nil)),
	}}

	choices, err := Resolve(source, Plan{
		Target: "linux-x86_64",
		Roots:  []Requirement{root("shape-tools", "^1")},
		Lock:   &Lock{Version: LockVersion},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := byName(choices)
	if len(got) != 2 || got["geom"].Version != "0.3.1" {
		t.Fatalf("choices = %+v, want geom 0.3.1 pulled in", choices)
	}
	if got["shape-tools"].Dependencies["geom"] != "0.3.1" {
		t.Errorf("dependencies = %+v, want the version geom resolved to", got["shape-tools"].Dependencies)
	}
}

// One version per name: the highest that satisfies both requirers.
func TestResolveSettlesOneVersionForTwoRequirers(t *testing.T) {
	source := &fakeSource{packages: map[string]Package{
		"a":    pkg("a", release("1.0.0", Requirements{"geom": "^0.3"}, nil)),
		"b":    pkg("b", release("1.0.0", Requirements{"geom": ">=0.3.5, <0.4"}, nil)),
		"geom": pkg("geom", release("0.3.9", nil, nil), release("0.3.6", nil, nil), release("0.3.1", nil, nil)),
	}}

	choices, err := Resolve(source, Plan{
		Target: "linux-x86_64",
		Roots:  []Requirement{root("a", "^1"), root("b", "^1")},
		Lock:   &Lock{Version: LockVersion},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := byName(choices)["geom"].Version; got != "0.3.9" {
		t.Errorf("geom = %s, want 0.3.9, the highest satisfying both", got)
	}
}

func TestResolveNamesBothSidesOfAConflict(t *testing.T) {
	source := &fakeSource{packages: map[string]Package{
		"a":    pkg("a", release("1.0.0", Requirements{"geom": "^0.3"}, nil)),
		"b":    pkg("b", release("1.0.0", Requirements{"geom": "^0.5"}, nil)),
		"geom": pkg("geom", release("0.5.0", nil, nil), release("0.3.1", nil, nil)),
	}}

	_, err := Resolve(source, Plan{
		Target: "linux-x86_64",
		Roots:  []Requirement{root("a", "^1"), root("b", "^1")},
		Lock:   &Lock{Version: LockVersion},
	})
	if err == nil {
		t.Fatal("Resolve settled a requirement no version satisfies")
	}
	for _, want := range []string{"a 1.0.0", "^0.3", "b 1.0.0", "^0.5", "0.5.0, 0.3.1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to mention %q", err, want)
		}
	}
}

func TestResolveChecksWhatTheHostCanGive(t *testing.T) {
	source := &fakeSource{packages: map[string]Package{
		"shape-tools": pkg("shape-tools",
			release("2.0.0", nil, Requirements{"lumenc": ">=1"}),
			release("1.0.0", nil, Requirements{"lumenc": ">=0.2"}),
		),
	}}
	plan := Plan{
		Target: "linux-x86_64",
		Roots:  []Requirement{root("shape-tools", "*")},
		Lock:   &Lock{Version: LockVersion},
	}

	// 2.0.0 wants a host this one is not.
	plan.Hosts = map[string]string{"lumenc": "0.5.0"}
	choices, err := Resolve(source, plan)
	if err != nil {
		t.Fatal(err)
	}
	if choices[0].Version != "1.0.0" {
		t.Errorf("version = %s, want 1.0.0, the one this host can run", choices[0].Version)
	}

	// A requires naming a host nobody passed is unsatisfied.
	plan.Hosts = nil
	_, err = Resolve(source, plan)
	if err == nil || !strings.Contains(err.Error(), "no --host lumenc was passed") {
		t.Errorf("err = %v, want it to report the missing host", err)
	}

	// And a host too old for anything published.
	plan.Hosts = map[string]string{"lumenc": "0.1.0"}
	_, err = Resolve(source, plan)
	if err == nil || !strings.Contains(err.Error(), "you have lumenc 0.1.0") {
		t.Errorf("err = %v, want it to report the host version", err)
	}
}

func TestResolveNeedsAnArtifactForTheTarget(t *testing.T) {
	windowsOnly := Release{
		Version: "1.0.0",
		Artifacts: []Artifact{{
			Target: "windows-x86_64", URL: "https://example.test/w.zip",
			SHA256: strings.Repeat("a", 64), Size: 8,
		}},
	}
	source := &fakeSource{packages: map[string]Package{
		"shape-tools": pkg("shape-tools", windowsOnly),
	}}

	_, err := Resolve(source, Plan{
		Target: "linux-x86_64",
		Roots:  []Requirement{root("shape-tools", "*")},
		Lock:   &Lock{Version: LockVersion},
	})
	if err == nil || !strings.Contains(err.Error(), "no artifact for linux-x86_64 or any") {
		t.Errorf("err = %v, want it to report the missing artifact", err)
	}
}

// A target-specific artifact beats the one that runs anywhere.
func TestResolvePrefersTheTargetArtifact(t *testing.T) {
	both := Release{
		Version: "1.0.0",
		Artifacts: []Artifact{
			{Target: AnyTarget, URL: "https://example.test/any.tar.gz", SHA256: strings.Repeat("a", 64), Size: 8},
			{Target: "linux-x86_64", URL: "https://example.test/linux.tar.gz", SHA256: strings.Repeat("b", 64), Size: 8},
		},
	}
	source := &fakeSource{packages: map[string]Package{"shape-tools": pkg("shape-tools", both)}}

	choices, err := Resolve(source, Plan{
		Target: "linux-x86_64",
		Roots:  []Requirement{root("shape-tools", "*")},
		Lock:   &Lock{Version: LockVersion},
	})
	if err != nil {
		t.Fatal(err)
	}
	if choices[0].Artifact.Target != "linux-x86_64" {
		t.Errorf("artifact = %+v, want the linux-x86_64 one", choices[0].Artifact)
	}
}

func TestResolveKeepsAPinThatStillSatisfies(t *testing.T) {
	source := &fakeSource{packages: map[string]Package{
		"shape-tools": pkg("shape-tools", release("1.9.0", nil, nil), release("1.2.3", nil, nil)),
	}}
	lock := &Lock{Version: LockVersion, Packages: []LockedPackage{{
		Name: "shape-tools", Version: "1.2.3", Platform: "lumen",
		Artifacts: map[string]string{AnyTarget: "sha256:x"},
	}}}
	plan := Plan{
		Target: "linux-x86_64",
		Roots:  []Requirement{root("shape-tools", "^1")},
		Lock:   lock,
	}

	choices, err := Resolve(source, plan)
	if err != nil {
		t.Fatal(err)
	}
	if choices[0].Version != "1.2.3" {
		t.Errorf("version = %s, want the pin 1.2.3", choices[0].Version)
	}

	// update moves the pin it is told to move.
	plan.Refresh = map[string]bool{"shape-tools": true}
	choices, err = Resolve(source, plan)
	if err != nil {
		t.Fatal(err)
	}
	if choices[0].Version != "1.9.0" {
		t.Errorf("version = %s, want 1.9.0 once the pin is released", choices[0].Version)
	}

	// update with no names moves everything.
	plan.Refresh = nil
	plan.RefreshAll = true
	choices, err = Resolve(source, plan)
	if err != nil {
		t.Fatal(err)
	}
	if choices[0].Version != "1.9.0" {
		t.Errorf("version = %s, want 1.9.0 under RefreshAll", choices[0].Version)
	}
}

// A pin the requirements have outgrown is dropped rather than obeyed.
func TestResolveDropsAPinThatNoLongerSatisfies(t *testing.T) {
	source := &fakeSource{packages: map[string]Package{
		"shape-tools": pkg("shape-tools", release("2.0.0", nil, nil), release("1.2.3", nil, nil)),
	}}
	lock := &Lock{Version: LockVersion, Packages: []LockedPackage{{
		Name: "shape-tools", Version: "1.2.3",
	}}}

	choices, err := Resolve(source, Plan{
		Target: "linux-x86_64",
		Roots:  []Requirement{root("shape-tools", "^2")},
		Lock:   lock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if choices[0].Version != "2.0.0" {
		t.Errorf("version = %s, want 2.0.0", choices[0].Version)
	}
}

// A package that a moved dependency no longer reaches drops out of the graph.
func TestResolveDropsWhatTheGraphNoLongerReaches(t *testing.T) {
	source := &fakeSource{packages: map[string]Package{
		"app": pkg("app",
			release("2.0.0", nil, nil),
			release("1.0.0", Requirements{"legacy": "^1"}, nil),
		),
		"legacy": pkg("legacy", release("1.0.0", nil, nil)),
	}}
	lock := &Lock{Version: LockVersion, Packages: []LockedPackage{{
		Name: "app", Version: "1.0.0",
	}}}
	plan := Plan{
		Target: "linux-x86_64",
		Roots:  []Requirement{root("app", "*")},
		Lock:   lock,
	}

	choices, err := Resolve(source, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(choices) != 2 {
		t.Fatalf("choices = %+v, want app 1.0.0 and legacy", choices)
	}

	plan.RefreshAll = true
	choices, err = Resolve(source, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(choices) != 1 || choices[0].Version != "2.0.0" {
		t.Errorf("choices = %+v, want app 2.0.0 alone", choices)
	}
}

func TestResolveReportsWhatTheRegistryCannotAnswer(t *testing.T) {
	source := &fakeSource{packages: map[string]Package{}}

	_, err := Resolve(source, Plan{
		Target: "linux-x86_64",
		Roots:  []Requirement{root("absent", "^1")},
		Lock:   &Lock{Version: LockVersion},
	})
	if err == nil || !strings.Contains(err.Error(), "absent (required by the command line)") {
		t.Errorf("err = %v, want it to name the package and who asked", err)
	}
}

func TestResolveReportsAPackageWithNoUsableReleases(t *testing.T) {
	source := &fakeSource{packages: map[string]Package{
		"empty": pkg("empty"),
		// A version lpm cannot order is a version it cannot pick.
		"odd": pkg("odd", Release{Version: "1.2", Artifacts: []Artifact{{Target: AnyTarget}}}),
	}}

	for name, want := range map[string]string{
		"empty": "published no releases",
		"odd":   "no version of odd satisfies",
	} {
		_, err := Resolve(source, Plan{
			Target: "linux-x86_64",
			Roots:  []Requirement{root(name, "*")},
			Lock:   &Lock{Version: LockVersion},
		})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, want)
		}
	}
}

func TestResolveRejectsRequirementsItCannotRead(t *testing.T) {
	source := &fakeSource{packages: map[string]Package{
		"shape-tools": pkg("shape-tools", release("1.0.0", nil, nil)),
	}}

	_, err := Resolve(source, Plan{
		Target: "linux-x86_64",
		Roots:  []Requirement{root("shape-tools", "not a version")},
		Lock:   &Lock{Version: LockVersion},
	})
	if err == nil || !strings.Contains(err.Error(), "the command line requires shape-tools") {
		t.Errorf("err = %v, want it to name the requirer", err)
	}

	_, err = Resolve(source, Plan{
		Target: "linux-x86_64",
		Hosts:  map[string]string{"lumenc": "not a version"},
		Roots:  []Requirement{root("shape-tools", "*")},
		Lock:   &Lock{Version: LockVersion},
	})
	if err == nil || !strings.Contains(err.Error(), "--host lumenc") {
		t.Errorf("err = %v, want it to name the host flag", err)
	}
}

// A release whose own requires cannot be parsed is passed over, not obeyed.
func TestResolveReportsAnUnreadableHostRequirement(t *testing.T) {
	source := &fakeSource{packages: map[string]Package{
		"shape-tools": pkg("shape-tools", release("1.0.0", nil, Requirements{"lumenc": "not a version"})),
	}}

	_, err := Resolve(source, Plan{
		Target: "linux-x86_64",
		Hosts:  map[string]string{"lumenc": "1.0.0"},
		Roots:  []Requirement{root("shape-tools", "*")},
		Lock:   &Lock{Version: LockVersion},
	})
	if err == nil || !strings.Contains(err.Error(), "not a version requirement") {
		t.Errorf("err = %v, want it to report the unreadable requirement", err)
	}
}

// Resolution asks the registry once per package, not once per requirement.
func TestResolveAsksForEachPackageOnce(t *testing.T) {
	source := &fakeSource{packages: map[string]Package{
		"a":    pkg("a", release("1.0.0", Requirements{"geom": "^0.3"}, nil)),
		"b":    pkg("b", release("1.0.0", Requirements{"geom": "^0.3"}, nil)),
		"geom": pkg("geom", release("0.3.1", nil, nil)),
	}}

	if _, err := Resolve(source, Plan{
		Target: "linux-x86_64",
		Roots:  []Requirement{root("a", "^1"), root("b", "^1")},
		Lock:   &Lock{Version: LockVersion},
	}); err != nil {
		t.Fatal(err)
	}
	if source.asked["geom"] != 1 {
		t.Errorf("geom was fetched %d times, want 1", source.asked["geom"])
	}
}

func TestLockSourceAnswersFromTheLockAlone(t *testing.T) {
	lock := sampleLock()
	source := LockSource{Lock: lock}

	got, err := source.GetPackage("shape-tools")
	if err != nil {
		t.Fatal(err)
	}
	if got.Platform != "lumen" || len(got.Releases) != 1 || got.Releases[0].Version != "1.2.3" {
		t.Fatalf("package = %+v, want the pin", got)
	}
	if got.Releases[0].Dependencies["geom"] != "=0.3.1" {
		t.Errorf("dependencies = %+v, want geom pinned exactly", got.Releases[0].Dependencies)
	}
	artifact, ok := got.Releases[0].Pick("linux-x86_64")
	if !ok || artifact.SHA256 != "0123" {
		t.Errorf("artifact = %+v, %v, want the digest without its prefix", artifact, ok)
	}

	_, err = source.GetPackage("absent")
	if err == nil || ExitCodeFor(err) != ExitOffline {
		t.Errorf("err = %v (code %d), want the offline code", err, ExitCodeFor(err))
	}
}

func TestResolveOfflineWalksTheLock(t *testing.T) {
	lock := sampleLock()

	choices, err := Resolve(LockSource{Lock: lock}, Plan{
		Target: "linux-x86_64",
		Roots:  []Requirement{root("shape-tools", "^1.2")},
		Lock:   lock,
	})
	if err != nil {
		t.Fatal(err)
	}

	got := byName(choices)
	if len(got) != 2 || got["geom"].Version != "0.3.1" {
		t.Fatalf("choices = %+v, want shape-tools and geom from the lock", choices)
	}
	if got["shape-tools"].Artifact.Target != "linux-x86_64" {
		t.Errorf("artifact = %+v, want the target this machine verified", got["shape-tools"].Artifact)
	}
}

func TestPickReportsAnArtifactlessRelease(t *testing.T) {
	if _, ok := (Release{}).Pick("linux-x86_64"); ok {
		t.Error("Pick found an artifact in a release that has none")
	}
}
