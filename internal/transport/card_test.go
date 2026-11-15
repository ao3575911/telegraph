package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/handshake"
	"github.com/ao3575911/telegraph/internal/tlog"
)

func TestCardReturnsCopyableBioLine(t *testing.T) {
	r, st := regResponder(t), memStore()
	send(t, r, st, 42, "/register adz")
	e, ok := resolved(t, st, "adz@nym")
	if !ok {
		t.Fatal("adz not registered")
	}
	want := "⟁ adz@nym · " + e.HashHex()[:8]
	got := send(t, r, st, 42, "/card")
	if !strings.Contains(got, "<code>"+want+"</code>") {
		t.Fatalf("/card lacks the bio line in a code entity:\n%s", got)
	}
	// The node attests the line once, so `telegraph scan` reports VERIFIED.
	if !handshake.VerifyCard(want, "adz@nym", st.Entries) {
		t.Fatal("card line not attested by the owner key")
	}
	n := len(st.Entries)
	send(t, r, st, 42, "/card adz")
	if len(st.Entries) != n {
		t.Fatal("a repeat /card signed a second attestation")
	}
	// Works in a group too, as a reply, for the sender's own handles.
	rep, ok := r.Handle(st, Incoming{Chat: -5, User: 42, ID: 9, Text: "/card@TelegraphTestBot"})
	if !ok || !strings.Contains(rep.HTML, want) || rep.ReplyTo != 9 {
		t.Fatalf("group /card: %+v", rep)
	}
}

func TestCardWithoutHandle(t *testing.T) {
	r, st := regResponder(t), memStore()
	if got := send(t, r, st, 7, "/card"); !strings.Contains(got, "no handle") || !strings.Contains(got, "/register") {
		t.Fatalf("no-handle reply: %s", got)
	}
	send(t, r, st, 42, "/register adz")
	if got := send(t, r, st, 7, "/card adz"); !strings.Contains(got, "You do not hold") {
		t.Fatalf("another user's handle: %s", got)
	}
	if got := send(t, r, st, 7, "/card a b"); !strings.Contains(got, "Usage") {
		t.Fatalf("bad input: %s", got)
	}
	send(t, r, st, 42, "/revoke adz")
	if got := send(t, r, st, 42, "/card"); !strings.Contains(got, "no handle") {
		t.Fatalf("after revoke: %s", got)
	}
	off := &Responder{}
	if got := send(t, off, st, 42, "/card"); !strings.Contains(got, "telegraph card") {
		t.Fatalf("registration off: %s", got)
	}
}

func TestCardListsEveryHandle(t *testing.T) {
	r, st := regResponder(t), memStore()
	send(t, r, st, 42, "/register a1")
	send(t, r, st, 42, "/register a2")
	got := send(t, r, st, 42, "/card")
	if strings.Count(got, "<code>⟁ ") != 2 {
		t.Fatalf("want two bio lines:\n%s", got)
	}
}

// TestBotRefusesTakeover (#15): the bot never signs for a handle another
// key holds, even when that key registered after the bot's user asked.
func TestBotRefusesTakeover(t *testing.T) {
	r, st := regResponder(t), memStore()
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	e, _ := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: "bob@nym", DID: "did:bob",
		TS: time.Now().Unix() - 60, Nonce: []byte{1}}, other, nil)
	st.AppendEntry(e)
	n := len(st.Entries)
	for _, cmd := range []string{"/register bob", "/register bob did:key:z6MkX", "/revoke bob"} {
		if got := send(t, r, st, 42, cmd); !strings.Contains(got, "another key") {
			t.Errorf("%s: %s", cmd, got)
		}
	}
	if len(st.Entries) != n {
		t.Fatal("the bot signed an entry for a handle another key owns")
	}
	// A stale custody record (handle swapped away) neither blocks nor counts.
	st.Custody = map[string]int64{"bob@nym": 42}
	if held(st, r.pub(), 42, "") != 0 || len(heldHandles(st, r.pub(), 42)) != 0 {
		t.Fatal("custody of a foreign-owned handle counted as held")
	}
	send(t, r, st, 42, "/register bob")
	if _, ok := st.Custody["bob@nym"]; ok {
		t.Fatal("stale custody record kept")
	}
}

// TestIngestRefusesBackdatedTakeover: a frame carrying a register dated
// before the current owner's tenure is refused and never republished.
func TestIngestRefusesBackdatedTakeover(t *testing.T) {
	st := memStore()
	_, a, _ := ed25519.GenerateKey(rand.Reader)
	_, b, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now().Unix()
	own, _ := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: "x@nym", DID: "did:a", TS: now - 100, Nonce: []byte{1}}, a, nil)
	st.AppendReceived(own)
	sy := NewSyncer(nil, nil, st, b)
	back, _ := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: "x@nym", DID: "did:b", TS: now - 200, Nonce: []byte{2}}, b, nil)
	if _, err := sy.IngestText(oneFrame(t, back)); err == nil {
		t.Fatal("backdated takeover admitted")
	}
	if len(st.Entries) != 1 || len(st.Pending()) != 0 {
		t.Fatalf("store changed: %d entries, %d pending", len(st.Entries), len(st.Pending()))
	}
	if e, _, _ := lookupHandle(st, "x@nym"); e.Payload.DID != "did:a" {
		t.Fatal("owner changed")
	}
}

func TestDescriptionsFitTelegramLimits(t *testing.T) {
	for _, r := range []*Responder{regResponder(t), {Version: "v0.3.0"}} {
		short, long := r.Descriptions()
		if n := len([]rune(short)); n > 120 || n < 10 {
			t.Errorf("short description %d chars", n)
		}
		if n := len([]rune(long)); n > 512 {
			t.Errorf("description %d chars", n)
		}
		if !strings.Contains(long, "@tgraph_net") || strings.Contains(long, "t.me/+") {
			t.Errorf("description channel link: %s", long)
		}
		if (r.Priv != nil) != strings.Contains(long, "/register") {
			t.Errorf("description /register mention vs registration on: %s", long)
		}
	}
}

func TestSetupDescriptionsSkipsUnchanged(t *testing.T) {
	api := &fakeBotAPI{}
	sy, bot, stop := testSyncer(t, api, memStore())
	defer stop()
	short, long := sy.DM.Descriptions()
	api.hook = func(method string, _ map[string]any) (int, any, bool) {
		switch method {
		case "getMyShortDescription":
			return 200, map[string]any{"ok": true, "result": map[string]any{"short_description": short}}, true
		case "getMyDescription":
			return 200, map[string]any{"ok": true, "result": map[string]any{"description": "old"}}, true
		}
		return 0, nil, false
	}
	if err := bot.SetupDescriptions(short, long); err != nil {
		t.Fatal(err)
	}
	if len(api.methods("setMyShortDescription")) != 0 {
		t.Error("unchanged short description was re-sent")
	}
	if d := api.methods("setMyDescription"); len(d) != 1 || d[0]["description"] != long {
		t.Errorf("description not updated: %v", d)
	}
}
