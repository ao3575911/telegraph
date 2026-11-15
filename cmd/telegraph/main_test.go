package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ao3575911/telegraph/internal/ui"
)

func TestHelpBannerOnlyWithColour(t *testing.T) {
	var plain, color bytes.Buffer
	help(ui.New(&plain, false))
	help(ui.New(&color, true))
	if strings.Contains(plain.String(), "⟁") || strings.Contains(plain.String(), "\x1b") {
		t.Fatalf("plain help has banner or colour:\n%s", plain.String())
	}
	if !strings.Contains(color.String(), "⟁") || !strings.Contains(color.String(), "v"+Version) {
		t.Fatal("coloured help lacks the banner")
	}
	if !strings.Contains(plain.String(), "usage:") {
		t.Fatal("usage missing")
	}
}

// Build the binary and check the banner never appears on other commands
// or when output is piped, even with CLICOLOR_FORCE, unless asked for help.
func TestBinaryBannerScope(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "telegraph")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	run := func(env []string, args ...string) string {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), append(env, "TELEGRAPH_HOME="+t.TempDir())...)
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	if s := run([]string{"NO_COLOR="}, "help"); strings.Contains(s, "⟁") {
		t.Fatal("banner printed to a pipe")
	}
	if s := run([]string{"NO_COLOR=", "CLICOLOR_FORCE=1"}, "help"); !strings.Contains(s, "⟁") {
		t.Fatal("forced colour help lacks banner")
	}
	if s := run([]string{"NO_COLOR=1", "CLICOLOR_FORCE=1"}, "help"); strings.Contains(s, "⟁") || strings.Contains(s, "\x1b") {
		t.Fatal("NO_COLOR ignored")
	}
	if s := run([]string{"NO_COLOR=", "CLICOLOR_FORCE=1"}, "init"); strings.Contains(s, "⟁") {
		t.Fatal("banner on a non-help command")
	}
	if s := run(nil, "version"); strings.TrimSpace(s) != "telegraph "+Version {
		t.Fatalf("version: %q", s)
	}
}
