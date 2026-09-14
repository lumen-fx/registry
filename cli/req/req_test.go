package req

import (
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
)

func matches(t *testing.T, spec, version string) bool {
	t.Helper()

	constraint, err := Parse(spec)
	if err != nil {
		t.Fatalf("Parse(%q) = %v", spec, err)
	}
	parsed, err := semver.StrictNewVersion(version)
	if err != nil {
		t.Fatalf("StrictNewVersion(%q) = %v", version, err)
	}
	return constraint.Check(parsed)
}

func TestParseFollowsCargo(t *testing.T) {
	for _, c := range []struct {
		spec   string
		hits   []string
		misses []string
	}{
		{"^1.2", []string{"1.2.0", "1.9.9"}, []string{"1.1.0", "2.0.0"}},
		// A bare requirement is a caret requirement. Semver on its own would
		// read this one as ~1.2 and stop at 1.3.0.
		{"1.2", []string{"1.2.0", "1.9.9"}, []string{"1.1.0", "2.0.0"}},
		{"1.2.3", []string{"1.2.3", "1.9.9"}, []string{"1.2.2", "2.0.0"}},
		{"~1.2", []string{"1.2.0", "1.2.9"}, []string{"1.3.0"}},
		{"=1.2.3", []string{"1.2.3"}, []string{"1.2.4"}},
		{">=1, <2", []string{"1.0.0", "1.9.9"}, []string{"0.9.0", "2.0.0"}},
		{">=1,<2", []string{"1.0.0"}, []string{"2.0.0"}},
		{"1.2.*", []string{"1.2.0", "1.2.9"}, []string{"1.3.0"}},
		{"1.2.x", []string{"1.2.0"}, []string{"1.3.0"}},
		{"*", []string{"0.1.0", "9.9.9"}, nil},
		{"^0.3", []string{"0.3.0", "0.3.9"}, []string{"0.4.0"}},
		{"^0.0.3", []string{"0.0.3"}, []string{"0.0.4"}},
		{"^1 || ^3", []string{"1.5.0", "3.1.0"}, []string{"2.0.0"}},
		{"  ^1.2  ", []string{"1.2.0"}, []string{"2.0.0"}},
	} {
		for _, version := range c.hits {
			if !matches(t, c.spec, version) {
				t.Errorf("%q should match %s", c.spec, version)
			}
		}
		for _, version := range c.misses {
			if matches(t, c.spec, version) {
				t.Errorf("%q should not match %s", c.spec, version)
			}
		}
	}
}

// A pre-release is matched only by a requirement that names one.
func TestParseKeepsPreReleasesOut(t *testing.T) {
	if matches(t, "^1.2", "1.3.0-rc.1") {
		t.Error("^1.2 matched a pre-release")
	}
	if !matches(t, ">=1.3.0-rc.1, <2", "1.3.0-rc.1") {
		t.Error("a requirement naming a pre-release did not match it")
	}
}

func TestParseRejectsWhatItCannotRead(t *testing.T) {
	for _, spec := range []string{"", "   ", "not a version", "^", ">=", "1.2.3.4.5", "^^1"} {
		if _, err := Parse(spec); err == nil {
			t.Errorf("Parse(%q) = nil, want an error", spec)
		}
	}

	err := func() error { _, err := Parse("not a version"); return err }()
	if !strings.Contains(err.Error(), "not a version requirement") {
		t.Errorf("err = %q, want it to say what was wrong", err)
	}
	if _, err := Parse("  "); err == nil || !strings.Contains(err.Error(), "is required") {
		t.Errorf("Parse(blank) = %v, want it to report a missing requirement", err)
	}
}

func TestNormalizeOnlyTouchesBareTerms(t *testing.T) {
	for _, c := range []struct{ spec, want string }{
		{"1.2", "^1.2"},
		{"1.2.3", "^1.2.3"},
		{"^1.2", "^1.2"},
		{"~1.2", "~1.2"},
		{"=1.2.3", "=1.2.3"},
		{">=1, <2", ">=1, <2"},
		{">=1,<2", ">=1, <2"},
		{"*", "*"},
		{"1.2.*", "1.2.*"},
		{"1.2.x", "1.2.x"},
		{"1.2.X", "1.2.X"},
		{"1 || 3", "^1 || ^3"},
		{"", ""},
	} {
		if got := Normalize(c.spec); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.spec, got, c.want)
		}
	}
}
