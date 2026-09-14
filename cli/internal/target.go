package internal

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
)

// AnyTarget is the artifact that runs everywhere. A package publishes it when
// it ships no compiled code, or alongside per-target builds as a fallback.
const AnyTarget = "any"

// Targets is the vocabulary an artifact, a lock entry, and `--target` all
// share. One spelling per platform and architecture, so the same name means
// the same machine wherever it is written down.
var Targets = []string{
	"linux-x86_64",
	"linux-aarch64",
	"macos-x86_64",
	"macos-aarch64",
	"windows-x86_64",
	"windows-aarch64",
	AnyTarget,
}

// ValidTarget reports whether name is in the vocabulary, `any` included.
func ValidTarget(name string) bool {
	return slices.Contains(Targets, name)
}

// CheckTarget rejects anything `--target` cannot mean. `any` is what a
// release publishes, never what a machine is.
func CheckTarget(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("--target is required; this machine is %s", HostTarget())
	case name == AnyTarget:
		return fmt.Errorf("--target names the machine to install for, so it cannot be %q", AnyTarget)
	case !ValidTarget(name):
		return fmt.Errorf("unknown target %q; use one of %s", name, strings.Join(Targets[:len(Targets)-1], ", "))
	}
	return nil
}

// HostTarget names the machine lpm is running on. It is what an error
// message suggests; the host CLI still passes `--target` itself, because it
// may be installing for a machine that is not this one.
func HostTarget() string {
	var os string
	switch runtime.GOOS {
	case "linux":
		os = "linux"
	case "darwin":
		os = "macos"
	case "windows":
		os = "windows"
	default:
		return "unknown"
	}

	var arch string
	switch runtime.GOARCH {
	case "amd64":
		arch = "x86_64"
	case "arm64":
		arch = "aarch64"
	default:
		return "unknown"
	}

	return os + "-" + arch
}
