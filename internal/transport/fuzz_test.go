package transport

import (
	"crypto/ed25519"
	"strings"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/store"
)

// FuzzAssembler feeds arbitrary frame sequences (newline separated) to
// one Assembler. It must never panic or allocate per a hostile total.
func FuzzAssembler(f *testing.F) {
	f.Add("TGP1|ab|0|2|aGVs\nTGP1|ab|1|2|bG8=")
	f.Add("TGP1|x|0|3|AA==\nTGP1|x|5|10|AA==")
	f.Add("TGP1|x|0|2147483647|AA==")
	f.Add("TGP1|||||")
	f.Fuzz(func(t *testing.T, in string) {
		a := NewAssembler()
		for _, fr := range strings.Split(in, "\n") {
			_, _, _, _ = a.Add(fr)
		}
	})
}

// FuzzHandle sends arbitrary text to the bot as a DM and in a group. It
// must never panic and every reply must be non-empty HTML.
func FuzzHandle(f *testing.F) {
	for _, s := range []string{"/register adz", "/register adz did:key:z6Mk", "/revoke adz", "/resolve a@nym",
		"/start@bot x", "/\n", "/register\tadz", "alice@nym", "/stats", "/@", "/register 🙂", "/r\x00"} {
		f.Add(s, true)
	}
	seed := make([]byte, ed25519.SeedSize)
	priv := ed25519.NewKeyFromSeed(seed)
	f.Fuzz(func(t *testing.T, text string, private bool) {
		now := time.Unix(1_800_000_000, 0)
		r := &Responder{Version: "fuzz", BotUsername: "bot", Priv: priv, Limit: 1 << 20,
			Now: func() time.Time { return now }}
		st := &store.Store{Tips: map[string]string{}, Seen: map[string]bool{}, Peers: map[string]*store.Peer{}}
		for i := 0; i < 2; i++ {
			rep, ok := r.Handle(st, Incoming{Chat: 5, User: 5, Private: private, Text: text})
			if ok && rep.HTML == "" {
				t.Fatalf("empty reply to %q", text)
			}
		}
	})
}
