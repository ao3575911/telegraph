// Package ui renders compact, optionally coloured terminal output.
//
// Colour is on only when stdout is a terminal and NO_COLOR is unset or
// empty (https://no-color.org). CLICOLOR_FORCE=1 forces colour on for
// non-terminals (recordings, captured samples); NO_COLOR still wins.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	cyan   = "\x1b[36m"
)

// Marks used for every status line.
const (
	MarkOK   = "✓"
	MarkFail = "✗"
	MarkWarn = "!"
)

// ColorEnabled is the pure decision: NO_COLOR (non-empty) always disables;
// otherwise a terminal, or a non-empty CLICOLOR_FORCE other than "0",
// enables.
func ColorEnabled(noColor, force string, isTTY bool) bool {
	if noColor != "" {
		return false
	}
	if force != "" && force != "0" {
		return true
	}
	return isTTY
}

// IsTerminal reports whether f is a character device (a TTY).
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// FromEnv decides colour for f from the process environment.
func FromEnv(f *os.File) bool {
	return ColorEnabled(os.Getenv("NO_COLOR"), os.Getenv("CLICOLOR_FORCE"), IsTerminal(f))
}

// Printer writes styled lines to W.
type Printer struct {
	W     io.Writer
	Color bool
}

// New returns a Printer for w with colour on or off.
func New(w io.Writer, color bool) *Printer { return &Printer{W: w, Color: color} }

func (p *Printer) wrap(code, s string) string {
	if !p.Color || s == "" {
		return s
	}
	return code + s + reset
}

func (p *Printer) Green(s string) string  { return p.wrap(green, s) }
func (p *Printer) Red(s string) string    { return p.wrap(red, s) }
func (p *Printer) Yellow(s string) string { return p.wrap(yellow, s) }
func (p *Printer) Dim(s string) string    { return p.wrap(dim, s) }
func (p *Printer) Bold(s string) string   { return p.wrap(bold, s) }
func (p *Printer) Cyan(s string) string   { return p.wrap(cyan, s) }

// Mark returns the coloured ✓ / ✗ / ! for ok, fail and warn.
func (p *Printer) Mark(ok bool) string {
	if ok {
		return p.Green(MarkOK)
	}
	return p.Red(MarkFail)
}

func (p *Printer) line(mark, format string, a ...any) {
	fmt.Fprintf(p.W, "%s %s\n", mark, fmt.Sprintf(format, a...))
}

// OK, Fail, Warn and Info print one status line with a leading mark.
func (p *Printer) OK(format string, a ...any)   { p.line(p.Green(MarkOK), format, a...) }
func (p *Printer) Fail(format string, a ...any) { p.line(p.Red(MarkFail), format, a...) }
func (p *Printer) Warn(format string, a ...any) { p.line(p.Yellow(MarkWarn), format, a...) }
func (p *Printer) Info(format string, a ...any) { p.line(p.Dim("·"), format, a...) }

// Println prints a plain line.
func (p *Printer) Println(a ...any) { fmt.Fprintln(p.W, a...) }

// Printf prints formatted text.
func (p *Printer) Printf(format string, a ...any) { fmt.Fprintf(p.W, format, a...) }

// KV prints indented key/value rows with keys aligned and dimmed.
func (p *Printer) KV(rows [][2]string) {
	w := 0
	for _, r := range rows {
		if n := Width(r[0]); n > w {
			w = n
		}
	}
	for _, r := range rows {
		fmt.Fprintf(p.W, "  %s  %s\n", p.Dim(Pad(r[0], w)), r[1])
	}
}

// Table prints rows with every column padded to its widest visible cell.
// Cells may already contain colour codes.
func (p *Printer) Table(indent string, rows [][]string) {
	var widths []int
	for _, r := range rows {
		for i, c := range r {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			if n := Width(c); n > widths[i] {
				widths[i] = n
			}
		}
	}
	for _, r := range rows {
		var b strings.Builder
		b.WriteString(indent)
		for i, c := range r {
			if i == len(r)-1 {
				b.WriteString(c)
				break
			}
			b.WriteString(Pad(c, widths[i]))
			b.WriteString("  ")
		}
		fmt.Fprintln(p.W, strings.TrimRight(b.String(), " "))
	}
}

// Width is the visible width of s: runes, ignoring ANSI escape sequences.
func Width(s string) int {
	n, esc := 0, false
	for _, r := range s {
		switch {
		case esc:
			if r >= '@' && r <= '~' && r != '[' {
				esc = false
			}
		case r == '\x1b':
			esc = true
		default:
			n++
		}
	}
	return n
}

// Pad right-pads s with spaces to visible width w.
func Pad(s string, w int) string {
	if d := w - Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// Short returns the first n characters of a hex id (or all of it).
func Short(h string, n int) string {
	if utf8.RuneCountInString(h) > n {
		return h[:n]
	}
	return h
}

// Banner returns the 3-line ⟁ TELEGRAPH banner, or "" without colour:
// it is decoration for interactive terminals only.
func (p *Printer) Banner(version, tagline string) string {
	if !p.Color {
		return ""
	}
	rule := strings.Repeat("─", 44)
	return p.Cyan(bold+"⟁ ") + p.Bold("TELEGRAPH") + "  " + p.Dim("v"+version) + "\n" +
		p.Dim(tagline) + "\n" + p.Dim(rule) + "\n"
}
