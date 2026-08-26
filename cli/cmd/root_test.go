package cmd

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// Run prints with fmt.Println, so the test has to capture the process stdout
// rather than the command's out stream.
func TestExecuteWithoutArgsPrintsHelpHint(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	rootCmd.SetArgs(nil)
	if err := Execute(); err != nil {
		t.Errorf("Execute() with no args: %v", err)
	}

	w.Close()
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "--help") {
		t.Errorf("expected a hint pointing at --help, got %q", out)
	}
}

func TestExecuteReportsUnknownFlag(t *testing.T) {
	rootCmd.SetErr(io.Discard)
	rootCmd.SetOut(io.Discard)
	rootCmd.SetArgs([]string{"--definitely-not-a-flag"})
	if err := Execute(); err == nil {
		t.Error("Execute() accepted an unknown flag")
	}
}

func TestVersionFlagReportsStampedValues(t *testing.T) {
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetArgs([]string{"--version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{version, commit, date} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("version output %q is missing %q", buf.String(), want)
		}
	}
}
