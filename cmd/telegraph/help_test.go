package main

import (
	"encoding/json"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func unbase58(t *testing.T, s string) []byte {
	t.Helper()
	n := new(big.Int)
	for _, c := range s {
		i := strings.IndexRune(b58, c)
		if i < 0 {
			t.Fatalf("bad base58 %q", c)
		}
		n.Mul(n, big.NewInt(58))
		n.Add(n, big.NewInt(int64(i)))
	}
	return n.Bytes()
}

// did:key test vector from the W3C CCG did:key spec (Ed25519).
func TestKeyDIDVector(t *testing.T) {
	pub := unbase58(t, "4zvwRjXUKGfvwnParsHAS3HuSVzV5cA4McphgmoCtajS")
	if got := keyDID(pub); got != "did:key:z6MkiTBz1ymuepAQ4HEHYSF1H8quG5GLVVQR3djdX3mDooWp" {
		t.Fatalf("keyDID = %s", got)
	}
	if base58([]byte{0, 0, 1}) != "112" {
		t.Fatal("leading zeros")
	}
}

func buildBin(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "telegraph")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func run(t *testing.T, bin, home string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "TELEGRAPH_HOME="+home, "NO_COLOR=1", "TELEGRAPH_COMMITTEE=", "TELEGRAPH_PEERS=")
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	return string(out), code
}

// Every command has its own -h; --json output parses; register works
// without --did.
func TestBinaryHelpJSONAndDefaultDID(t *testing.T) {
	bin := buildBin(t)
	home := t.TempDir()
	for _, d := range cmdDocs {
		s, code := run(t, bin, home, d.name, "-h")
		if code != 0 || !strings.Contains(s, "usage: telegraph "+d.name) {
			t.Errorf("%s -h: exit %d\n%s", d.name, code, s)
		}
		if d.json != strings.Contains(s, "--json") {
			t.Errorf("%s -h: --json mention mismatch", d.name)
		}
	}
	if s, _ := run(t, bin, home, "help", "verify"); !strings.Contains(s, "usage: telegraph verify") {
		t.Errorf("help verify: %s", s)
	}
	run(t, bin, home, "init")
	if s, code := run(t, bin, home, "register", "--handle", "me@nym"); code != 0 || !strings.Contains(s, "did:key:z6Mk") {
		t.Fatalf("register without --did: %d %s", code, s)
	}
	for _, args := range [][]string{
		{"resolve", "me@nym", "--json"}, {"verify", "--json", "me@nym"}, {"audit", "--json"},
		{"net", "--json"}, {"committee", "--json"}, {"config", "--json"}, {"config", "key", "--json"},
	} {
		s, code := run(t, bin, home, args...)
		var v map[string]any
		if err := json.Unmarshal([]byte(s), &v); err != nil || code != 0 {
			t.Errorf("%v: exit %d, %v\n%s", args, code, err, s)
		}
	}
	s, code := run(t, bin, home, "resolve", "nobody@nym", "--json")
	var v map[string]any
	if code != 1 || json.Unmarshal([]byte(s), &v) != nil || v["registered"] != false {
		t.Errorf("unregistered --json: %d %s", code, s)
	}
	s, _ = run(t, bin, home, "resolve", "me@nym", "--json")
	_ = json.Unmarshal([]byte(s), &v)
	if !strings.HasPrefix(v["did"].(string), "did:key:z6Mk") || v["status"] != "REGISTERED" {
		t.Errorf("resolve json: %s", s)
	}
}
