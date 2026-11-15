package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// data.json files from before v0.3.1 kept raw Telegram user ids in
// custody. Open turns them into tags under a node-local secret, and the
// next Save leaves no raw id in the file.
func TestCustodyMigratesToTags(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.json")
	const raw = "987654321"
	if err := os.WriteFile(p, []byte(`{"entries":[],"tips":{},"seen":{},"custody":{"adz@nym":`+raw+`}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	tag := st.Custody["adz@nym"]
	if len(tag) != TagLen || tag != st.TelegramTag(987654321) || st.LegacyCustody != nil {
		t.Fatalf("custody not migrated: %v %v", st.Custody, st.LegacyCustody)
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), raw) {
		t.Fatalf("raw id still in data.json: %s", b)
	}
	fi, err := os.Stat(filepath.Join(dir, secretFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("secret file: %v %v", err, fi)
	}
	again, err := Open(p)
	if err != nil || again.Custody["adz@nym"] != tag || again.TelegramTag(987654321) != tag {
		t.Fatal("tag not stable across restarts")
	}
	other, _ := Open(filepath.Join(t.TempDir(), "data.json"))
	if other.TelegramTag(987654321) == tag {
		t.Fatal("two nodes derive the same tag: linkable")
	}
}
