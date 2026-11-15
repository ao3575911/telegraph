package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

// fakeBotAPI serves getUpdates from a queue (once) and records every
// sendMessage request.
type fakeBotAPI struct {
	mu      sync.Mutex
	updates []map[string]any
	sent    []map[string]any
}

func (f *fakeBotAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	f.mu.Lock()
	defer f.mu.Unlock()
	var result any = true
	switch {
	case strings.HasSuffix(r.URL.Path, "/getUpdates"):
		ups := f.updates
		if ups == nil {
			ups = []map[string]any{}
		}
		result, f.updates = ups, nil
	case strings.HasSuffix(r.URL.Path, "/sendMessage"):
		f.sent = append(f.sent, req)
		result = map[string]any{"message_id": len(f.sent)}
	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "Not Found"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func dmUpdate(id, chat int64, text string) map[string]any {
	return map[string]any{"update_id": id, "message": map[string]any{
		"message_id": id, "text": text, "chat": map[string]any{"id": chat, "type": "private"}}}
}

func signedEntry(t *testing.T, handle string) *tlog.Entry {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	dieOnT(t, err)
	e, err := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: handle, DID: "did:" + handle,
		TS: time.Now().Unix() - 10, Nonce: randData(t, 16)}, priv, nil)
	dieOnT(t, err)
	return e
}

func oneFrame(t *testing.T, e *tlog.Entry) string {
	t.Helper()
	enc, err := e.Encode()
	dieOnT(t, err)
	frs := SplitFrames(e.HashHex(), enc)
	if len(frs) != 1 {
		t.Fatalf("entry split into %d frames, want 1", len(frs))
	}
	return frs[0]
}

func TestDMRepliesOverFakeBotAPI(t *testing.T) {
	alice := signedEntry(t, "alice@nym")
	dmFrame := oneFrame(t, signedEntry(t, "evil@nym"))  // must never be ingested
	chFrame := oneFrame(t, signedEntry(t, "carol@nym")) // channel gossip, ingested

	api := &fakeBotAPI{updates: []map[string]any{
		dmUpdate(1, 11, "/start"),
		dmUpdate(2, 11, "/resolve alice@nym"),
		dmUpdate(3, 11, "/resolve <b>x</b>@nym"),
		dmUpdate(4, 22, dmFrame),
		dmUpdate(5, 22, "/pin"),
		dmUpdate(6, 22, "/status@telegraph_bot"),
		dmUpdate(7, 22, "/help"), // fourth message from chat 22: rate-limited
		{"update_id": 8, "channel_post": map[string]any{"message_id": 8, "text": chFrame,
			"chat": map[string]any{"id": -100, "type": "channel"}}},
	}}
	srv := httptest.NewServer(api)
	defer srv.Close()

	st := &store.Store{Seen: map[string]bool{}, Tips: map[string]string{}, Peers: map[string]*store.Peer{}}
	if !st.AppendReceived(alice) {
		t.Fatal("seed entry rejected")
	}
	st.Pin = "c700daaa0bac3c22"
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	dieOnT(t, err)
	bot := NewBot("TOKEN")
	bot.BaseURL = srv.URL
	sy := NewSyncer([]Conn{bot}, []string{"-100"}, st, priv)
	sy.DM = &Responder{Version: "v0.2.0", Channels: []string{"-100"}, Limit: 3, Window: time.Minute}

	if _, err := sy.PollWindow(0); err != nil {
		t.Fatal(err)
	}

	for _, e := range st.Entries {
		if e.Payload.Handle == "evil@nym" {
			t.Fatal("DM content entered the log")
		}
	}
	if len(st.Entries) != 2 {
		t.Fatalf("store has %d entries, want 2 (seed + channel post)", len(st.Entries))
	}

	api.mu.Lock()
	sent := api.sent
	api.mu.Unlock()
	if len(sent) != 6 {
		t.Fatalf("%d replies, want 6 (one per DM, minus one rate-limited)", len(sent))
	}
	want := []struct {
		chat string
		has  []string
	}{
		{"11", []string{"⟁ Telegraph", "go install github.com/ao3575911/telegraph", "--pin c700daaa0bac3c22",
			"TELEGRAPH_CHANNELS=-100", DefaultInviteURL, DefaultRepoURL, DefaultWikiURL}},
		{"11", []string{"<b>alice@nym</b>", "[REGISTERED]", "did:alice@nym", alice.KeyHex()[:8], "UNATTESTED"}},
		{"11", []string{"&lt;b&gt;x&lt;/b&gt;@nym", "not registered"}},
		{"22", []string{"/help"}},
		{"22", []string{"c700daaa0bac3c22"}},
		{"22", []string{"entries  1 (1 issuers)", "checkpoint  none yet"}}, // before the channel post
	}
	for i, w := range want {
		m := sent[i]
		if m["chat_id"] != w.chat || m["parse_mode"] != "HTML" {
			t.Fatalf("reply %d: chat %v parse_mode %v", i, m["chat_id"], m["parse_mode"])
		}
		text, _ := m["text"].(string)
		for _, h := range w.has {
			if !strings.Contains(text, h) {
				t.Errorf("reply %d lacks %q:\n%s", i, h, text)
			}
		}
		if strings.Contains(text, "<b>x</b>") {
			t.Errorf("reply %d echoes unescaped input", i)
		}
	}
}

func TestResponderRateLimitWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	r := &Responder{Limit: 2, Window: 10 * time.Second, Now: func() time.Time { return now }}
	st := &store.Store{}
	for i, want := range []bool{true, true, false} {
		if _, ok := r.Handle(st, 1, "/pin"); ok != want {
			t.Fatalf("message %d: ok=%v want %v", i, ok, want)
		}
	}
	if _, ok := r.Handle(st, 2, "/pin"); !ok {
		t.Fatal("limit leaked across chats")
	}
	now = now.Add(11 * time.Second)
	if _, ok := r.Handle(st, 1, "/pin"); !ok {
		t.Fatal("limit did not reset after the window")
	}
}

func TestDMIgnoredWithoutResponder(t *testing.T) {
	api := &fakeBotAPI{updates: []map[string]any{dmUpdate(1, 11, "/start")}}
	srv := httptest.NewServer(api)
	defer srv.Close()
	st := &store.Store{Seen: map[string]bool{}, Tips: map[string]string{}, Peers: map[string]*store.Peer{}}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	bot := NewBot("TOKEN")
	bot.BaseURL = srv.URL
	sy := NewSyncer([]Conn{bot}, []string{"-100"}, st, priv)
	if _, err := sy.PollWindow(0); err != nil {
		t.Fatal(err)
	}
	if len(api.sent) != 0 {
		t.Fatalf("%d replies without a responder", len(api.sent))
	}
}
