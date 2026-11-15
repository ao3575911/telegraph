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

// resolve exits 0 only when the handle resolves, so scripts such as
// scripts/node-join.sh can test it.
func TestBinaryResolveExitStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "telegraph")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	home := t.TempDir()
	run := func(args ...string) (string, int) {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "TELEGRAPH_HOME="+home, "NO_COLOR=1")
		out, err := cmd.CombinedOutput()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return string(out), code
	}
	if out, code := run("init"); code != 0 {
		t.Fatalf("init: %d %s", code, out)
	}
	if out, code := run("resolve", "nobody@nym"); code != 1 || !strings.Contains(out, "unregistered") {
		t.Fatalf("unregistered: exit %d %q", code, out)
	}
	if out, code := run("register", "--handle", "me@nym", "--did", "did:web:me.invalid"); code != 0 {
		t.Fatalf("register: %d %s", code, out)
	}
	if out, code := run("resolve", "me@nym"); code != 0 || !strings.Contains(out, "did:web:me.invalid") {
		t.Fatalf("registered: exit %d %q", code, out)
	}
	if out, code := run("revoke", "--handle", "me@nym"); code != 0 {
		t.Fatalf("revoke: %d %s", code, out)
	}
	if out, code := run("resolve", "me@nym"); code != 1 || !strings.Contains(out, "revoked") {
		t.Fatalf("revoked: exit %d %q", code, out)
	}
}
