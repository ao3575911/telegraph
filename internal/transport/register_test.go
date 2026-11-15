package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

func regResponder(t *testing.T) *Responder {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	dieOnT(t, err)
	now := time.Now().Truncate(time.Second)
	return &Responder{Version: "v0.2.2", Priv: priv, BotUsername: "TelegraphTestBot", Limit: 1000,
		Now: func() time.Time { return now }}
}

func send(t *testing.T, r *Responder, st *store.Store, user int64, text string) string {
	t.Helper()
	rep, ok := r.Handle(st, Incoming{Chat: user, User: user, Private: true, Text: text})
	if !ok {
		t.Fatalf("%q: no reply", text)
	}
	return rep.HTML
}

func resolved(t *testing.T, st *store.Store, h string) (tlog.Entry, bool) {
	t.Helper()
	reg, err := st.Registry()
	dieOnT(t, err)
	e, _, ok := reg.Resolve(h)
	return e, ok
}

func TestNormaliseName(t *testing.T) {
	for in, want := range map[string]string{
		"adz": "adz@nym", "Adz": "adz@nym", "adz@nym": "adz@nym", " a_b.c-1 ": "a_b.c-1@nym",
		"x": "x@nym", strings.Repeat("a", 32): strings.Repeat("a", 32) + "@nym",
	} {
		got, err := NormaliseName(in)
		if err != nil || got != want {
			t.Errorf("NormaliseName(%q) = %q, %v; want %q", in, got, err, want)
		}
		if tlog.ValidHandle(got) != nil {
			t.Errorf("%q fails the CLI handle rule", got)
		}
	}
	for _, bad := range []string{"", "_adz", "-a", "a b", "adz@other", "a@b@nym", "<b>", "🙂",
		strings.Repeat("a", 33), "adz@", "@nym"} {
		if got, err := NormaliseName(bad); err == nil {
			t.Errorf("NormaliseName(%q) = %q, want error", bad, got)
		}
	}
}

func TestParseRegister(t *testing.T) {
	h, d, def, err := parseRegister("adz", 42)
	if err != nil || h != "adz@nym" || d != "did:tg:42" || !def {
		t.Fatalf("default: %q %q %v %v", h, d, def, err)
	}
	h, d, def, err = parseRegister("adz did:key:z6MkTest", 42)
	if err != nil || h != "adz@nym" || d != "did:key:z6MkTest" || def {
		t.Fatalf("explicit: %q %q %v %v", h, d, def, err)
	}
	for _, bad := range []string{"", "a b c", "adz notadid", "adz did:", "adz did:key:", "adz did:KEY:x",
		"adz did:key:<b>", "adz did:key:" + strings.Repeat("x", 200)} {
		if _, _, _, err := parseRegister(bad, 42); err == nil {
			t.Errorf("parseRegister(%q) accepted", bad)
		}
	}
}

func TestRegisterSignsAndResolves(t *testing.T) {
	r, st := regResponder(t), memStore()
	got := send(t, r, st, 42, "/register adz")
	for _, want := range []string{"Registered <b>adz@nym</b>", "did:tg:42", "Custodial", "user id"} {
		if !strings.Contains(got, want) {
			t.Errorf("reply lacks %q: %s", want, got)
		}
	}
	e, ok := resolved(t, st, "adz@nym")
	if !ok || e.Payload.DID != "did:tg:42" || string(e.PubKey) != string(r.pub()) {
		t.Fatalf("not registered to the node key: %+v", e.Payload)
	}
	if st.Custody["adz@nym"] != 42 || len(st.Pending()) != 1 {
		t.Fatalf("custody %v, pending %d", st.Custody, len(st.Pending()))
	}
	// Same DID again: no new entry. New DID: updated, and it wins.
	if got := send(t, r, st, 42, "/register adz did:tg:42"); !strings.Contains(got, "already yours") {
		t.Errorf("repeat: %s", got)
	}
	if got := send(t, r, st, 42, "/register adz did:key:z6MkNew"); !strings.Contains(got, "Updated") {
		t.Errorf("update: %s", got)
	}
	if e, _ := resolved(t, st, "adz@nym"); e.Payload.DID != "did:key:z6MkNew" || len(st.Entries) != 2 {
		t.Fatalf("DID change lost: %q, %d entries", e.Payload.DID, len(st.Entries))
	}
}

func TestRegisterOwnerChecks(t *testing.T) {
	r, st := regResponder(t), memStore()
	send(t, r, st, 42, "/register adz")
	// Another Telegram user cannot take, change or revoke it.
	for _, cmd := range []string{"/register adz", "/register adz did:key:z6MkEvil", "/revoke adz"} {
		if got := send(t, r, st, 7, cmd); !strings.Contains(got, "another Telegram user") {
			t.Errorf("%s by stranger: %s", cmd, got)
		}
	}
	// A handle another key owns is refused.
	other := signedEntry(t, "bob@nym")
	st.AppendEntry(other)
	if got := send(t, r, st, 42, "/register bob"); !strings.Contains(got, "someone else") {
		t.Errorf("foreign handle: %s", got)
	}
	// A handle the node registered itself (CLI) has no custodian: refused.
	if _, err := r.sign(st, tlog.KindRegister, "op@nym", "did:web:op", nil); err != nil {
		t.Fatal(err)
	}
	if got := send(t, r, st, 42, "/register op"); !strings.Contains(got, "operator") {
		t.Errorf("operator handle: %s", got)
	}
	// The owner revokes; then anyone can register it.
	if got := send(t, r, st, 42, "/revoke adz"); !strings.Contains(got, "Revoked") {
		t.Fatalf("revoke: %s", got)
	}
	if e, _ := resolved(t, st, "adz@nym"); e.Payload.Kind != tlog.KindRevoke {
		t.Fatal("revoke not in the log")
	}
	if got := send(t, r, st, 7, "/register adz"); !strings.Contains(got, "Registered") {
		t.Errorf("after revoke: %s", got)
	}
	if got := send(t, r, st, 42, "/revoke zzz"); !strings.Contains(got, "not registered to you") {
		t.Errorf("revoke unknown: %s", got)
	}
}

func TestRegisterPerUserCap(t *testing.T) {
	r, st := regResponder(t), memStore()
	for _, n := range []string{"a1", "a2", "a3"} {
		send(t, r, st, 42, "/register "+n)
	}
	if got := send(t, r, st, 42, "/register a4"); !strings.Contains(got, "most one account") {
		t.Fatalf("cap: %s", got)
	}
	if got := send(t, r, st, 42, "/register a1 did:key:z6MkX"); !strings.Contains(got, "Updated") {
		t.Fatalf("update at cap: %s", got)
	}
}

func TestRegisterDMOnly(t *testing.T) {
	r, st := regResponder(t), memStore()
	for _, cmd := range []string{"/register@TelegraphTestBot adz", "/revoke@TelegraphTestBot adz"} {
		rep, ok := r.Handle(st, Incoming{Chat: -5, User: 42, ID: 9, Text: cmd})
		if !ok || !strings.Contains(rep.HTML, "only in a private chat") || rep.ReplyTo != 9 ||
			len(rep.Buttons) != 1 || rep.Buttons[0][0].URL != "https://t.me/TelegraphTestBot" {
			t.Errorf("%s in group: %v %+v", cmd, ok, rep)
		}
	}
	if _, ok := r.Handle(st, Incoming{Chat: -5, User: 42, Text: "/register adz"}); ok {
		t.Error("unaddressed group command answered")
	}
	if len(st.Entries) != 0 {
		t.Fatal("group command wrote an entry")
	}
}

func TestRegisterPromptAndDisabled(t *testing.T) {
	r, st := regResponder(t), memStore()
	rep, _ := r.Handle(st, Incoming{Chat: 42, User: 42, Private: true, Text: "/register"})
	if rep.ForceReply == "" || !strings.Contains(rep.HTML, "Which name?") {
		t.Fatalf("no prompt: %+v", rep)
	}
	if got := send(t, r, st, 42, "adz"); !strings.Contains(got, "Registered <b>adz@nym</b>") {
		t.Fatalf("prompt answer: %s", got)
	}
	off := &Responder{}
	for _, cmd := range []string{"/register adz", "/register", "/revoke adz"} {
		if got := send(t, off, st, 42, cmd); !strings.Contains(got, "does not register") {
			t.Errorf("%s with registration off: %s", cmd, got)
		}
	}
}

func TestRegisterRateLimited(t *testing.T) {
	r, st := regResponder(t), memStore()
	r.Limit = 1
	send(t, r, st, 42, "/register a1")
	rep, _ := r.Handle(st, Incoming{Chat: 42, User: 42, Private: true, Text: "/register a2"})
	if !strings.Contains(rep.HTML, "Too many messages") || len(st.Entries) != 1 {
		t.Fatalf("not rate-limited: %q, %d entries", rep.HTML, len(st.Entries))
	}
}

// End to end through the bot client: a DM /register is signed, published
// to the gossip channel and answered.
func TestRegisterPublishesToChannel(t *testing.T) {
	up := dm(1, 42, "/register adz")
	up["message"].(map[string]any)["from"] = map[string]any{"id": 42, "is_bot": false}
	api := &fakeBotAPI{batches: [][]map[string]any{{up}}}
	st := memStore()
	sy, _, stop := testSyncer(t, api, st)
	defer stop()
	sy.DM.Priv = sy.Priv
	sy.PollWindow(0)
	var frame, reply bool
	for _, m := range api.sent() {
		switch {
		case m["chat_id"] == "-100" && IsFrame(m["text"].(string)):
			frame = true
		case m["chat_id"] == "42" && strings.Contains(m["text"].(string), "Registered"):
			reply = true
		}
	}
	if !frame || !reply || st.Custody["adz@nym"] != 42 || len(st.Pending()) != 0 {
		t.Fatalf("frame %v reply %v custody %v pending %d", frame, reply, st.Custody, len(st.Pending()))
	}
}
