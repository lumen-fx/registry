package internal

import (
	"runtime"
	"strings"
	"testing"
)

func TestValidTargetCoversTheVocabulary(t *testing.T) {
	for _, target := range Targets {
		if !ValidTarget(target) {
			t.Errorf("ValidTarget(%q) = false, want true", target)
		}
	}
	for _, target := range []string{"", "linux", "linux-amd64", "Linux-x86_64", "solaris-sparc"} {
		if ValidTarget(target) {
			t.Errorf("ValidTarget(%q) = true, want false", target)
		}
	}
}

func TestCheckTargetRejectsWhatItCannotInstallFor(t *testing.T) {
	for _, c := range []struct{ target, want string }{
		{"", "required"},
		{AnyTarget, "cannot be"},
		{"linux-amd64", "unknown target"},
	} {
		err := CheckTarget(c.target)
		if err == nil {
			t.Errorf("CheckTarget(%q) = nil, want an error", c.target)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("CheckTarget(%q) = %q, want it to mention %q", c.target, err, c.want)
		}
	}

	if err := CheckTarget("linux-x86_64"); err != nil {
		t.Errorf("CheckTarget(linux-x86_64) = %v, want nil", err)
	}
}

// The suite runs on the platforms a release builds for, so the name this
// machine reports is checked on each of them.
func TestHostTargetNamesThisMachine(t *testing.T) {
	got := HostTarget()

	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		if !ValidTarget(got) || got == AnyTarget {
			t.Fatalf("HostTarget() = %q, want a target from the vocabulary", got)
		}
	default:
		if got != "unknown" {
			t.Fatalf("HostTarget() = %q on %s, want unknown", got, runtime.GOOS)
		}
		return
	}

	switch runtime.GOARCH {
	case "amd64":
		if !strings.HasSuffix(got, "-x86_64") {
			t.Errorf("HostTarget() = %q on amd64, want an x86_64 suffix", got)
		}
	case "arm64":
		if !strings.HasSuffix(got, "-aarch64") {
			t.Errorf("HostTarget() = %q on arm64, want an aarch64 suffix", got)
		}
	default:
		if got != "unknown" {
			t.Errorf("HostTarget() = %q on %s, want unknown", got, runtime.GOARCH)
		}
	}
}
