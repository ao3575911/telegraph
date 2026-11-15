package transport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/tlog"
)

// Nothing a node gossips or stores carries a raw Telegram user id: not
// the bot's default DID, not the frames on the bus, not data.json.
func TestNoRawTelegramIDsGossiped(t *testing.T) {
	const user, raw = int64(5550123987), "5550123987"
	dir := t.TempDir()
	bus := filepath.Join(dir, "bus.jsonl")
	n := newNode(t, dir, bus, "a")
	r := regResponder(t)
	r.Priv = n.sy.Priv
	for _, cmd := range []string{"/register adz", "/card", "/register bee", "/revoke bee"} {
		send(t, r, n.st, user, cmd)
	}
	n.sync(t)
	dieOnT(t, n.st.Save())
	for _, p := range []string{bus, filepath.Join(dir, "a.json")} {
		b, err := os.ReadFile(p)
		dieOnT(t, err)
		if strings.Contains(string(b), raw) {
			t.Fatalf("raw Telegram id in %s", filepath.Base(p))
		}
	}
	for i := range n.st.Entries {
		if IsRawTelegramDID(n.st.Entries[i].Payload.DID) {
			t.Fatal("raw id DID signed")
		}
	}
}

// redact-dids replaces a pre-v0.3.1 did:tg:<user id> DID with the user's
// tag DID through an owner update.
func TestRedactRawDIDs(t *testing.T) {
	r, st := regResponder(t), memStore()
	e, err := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: "old@nym", DID: "did:tg:777",
		TS: time.Now().Unix() - 100, Nonce: []byte{1}}, r.Priv, nil)
	dieOnT(t, err)
	st.AppendEntry(e)
	st.Custody = map[string]string{"old@nym": st.TelegramTag(777)}
	hs, err := RedactRawDIDs(st, r.Priv, time.Now(), false)
	if err != nil || len(hs) != 1 || len(st.Entries) != 1 {
		t.Fatalf("dry run: %v %v %d", hs, err, len(st.Entries))
	}
	if _, err := RedactRawDIDs(st, r.Priv, time.Now(), true); err != nil {
		t.Fatal(err)
	}
	got, _, _ := lookupHandle(st, "old@nym")
	if got.Payload.DID != TelegramDID(st.TelegramTag(777)) {
		t.Fatalf("DID now %q", got.Payload.DID)
	}
	if hs, _ := RedactRawDIDs(st, r.Priv, time.Now(), false); len(hs) != 0 {
		t.Fatal("still listed after apply")
	}
}
