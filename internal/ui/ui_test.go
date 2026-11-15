package ui

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestColorEnabled(t *testing.T) {
	cases := []struct {
		noColor, force string
		tty, want      bool
	}{
		{"", "", true, true},    // terminal: on
		{"", "", false, false},  // pipe/file: off
		{"1", "", true, false},  // NO_COLOR disables on a terminal
		{"x", "1", true, false}, // NO_COLOR beats CLICOLOR_FORCE
		{"", "1", false, true},  // CLICOLOR_FORCE enables for pipes
		{"", "0", false, false}, // CLICOLOR_FORCE=0 is not a force
		{"", "0", true, true},   // ...and does not disable a terminal
	}
	for _, c := range cases {
		if got := ColorEnabled(c.noColor, c.force, c.tty); got != c.want {
			t.Errorf("ColorEnabled(%q,%q,%v)=%v want %v", c.noColor, c.force, c.tty, got, c.want)
		}
	}
}

func TestIsTerminalOnRegularFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTerminal(f) {
		t.Fatal("regular file reported as a terminal")
	}
}

func TestFromEnvNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("CLICOLOR_FORCE", "1")
	if FromEnv(os.Stdout) {
		t.Fatal("NO_COLOR ignored")
	}
}

func TestPlainOutputHasNoEscapes(t *testing.T) {
	var b bytes.Buffer
	p := New(&b, false)
	p.OK("fine %d", 1)
	p.Fail("bad")
	p.Warn("hmm")
	p.KV([][2]string{{"a", p.Green("x")}, {"long", "y"}})
	if strings.Contains(b.String(), "\x1b") {
		t.Fatalf("escape codes with colour off: %q", b.String())
	}
	want := "✓ fine 1\n✗ bad\n! hmm\n  a     x\n  long  y\n"
	if b.String() != want {
		t.Fatalf("got %q want %q", b.String(), want)
	}
}

func TestColouredOutput(t *testing.T) {
	var b bytes.Buffer
	p := New(&b, true)
	p.OK("fine")
	if !strings.Contains(b.String(), green+MarkOK+reset) {
		t.Fatalf("no green mark: %q", b.String())
	}
}

func TestWidthIgnoresANSI(t *testing.T) {
	p := New(nil, true)
	if w := Width(p.Red("abc") + "⟁"); w != 4 {
		t.Fatalf("width %d", w)
	}
	if got := Pad(p.Dim("ab"), 4); Width(got) != 4 {
		t.Fatalf("pad width %d", Width(got))
	}
}

func TestTableAlignsColouredCells(t *testing.T) {
	var b bytes.Buffer
	p := New(&b, true)
	p.Table("", [][]string{{p.Green("a"), "1"}, {"bbb", "2"}})
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if Width(lines[0]) != Width(lines[1]) {
		t.Fatalf("misaligned: %q", lines)
	}
}

func TestBannerOnlyWithColour(t *testing.T) {
	if New(nil, false).Banner("0.1.0", "x") != "" {
		t.Fatal("banner printed without colour")
	}
	b := New(nil, true).Banner("0.1.0", "x")
	if !strings.Contains(b, "TELEGRAPH") || !strings.Contains(b, "v0.1.0") {
		t.Fatalf("banner: %q", b)
	}
	if n := strings.Count(b, "\n"); n < 3 || n > 5 {
		t.Fatalf("banner has %d lines", n)
	}
}
