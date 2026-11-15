package tlog

import (
	"strings"
	"testing"
)

func TestValidHandle(t *testing.T) {
	for _, ok := range []string{"alice@nym", "a@b", "Ünï@nym", strings.Repeat("a", 60) + "@nym"} {
		if err := ValidHandle(ok); err != nil {
			t.Errorf("ValidHandle(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "alice", "@nym", "alice@", "a@b@c", "a b@nym", "a\t@nym",
		strings.Repeat("a", 61) + "@nym"} {
		if ValidHandle(bad) == nil {
			t.Errorf("ValidHandle(%q) accepted", bad)
		}
	}
}
