package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

const testToken = "123456:SECRET-test-token"

// fakeBotAPI replays queued getUpdates batches and records every call.
type fakeBotAPI struct {
	mu      sync.Mutex
	batches [][]map[string]any
	calls   []call
	hook    func(method string, req map[string]any) (status int, body any, handled bool)
}

type call struct {
	method string
	req    map[string]any
}

func (f *fakeBotAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	method := r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:]
	if !strings.HasPrefix(r.URL.Path, "/bot"+testToken+"/") {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "Unauthorized"})
		return
	}
	f.mu.Lock()
	f.calls = append(f.calls, call{method, req})
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		if status, out, ok := hook(method, req); ok {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(out)
			return
		}
	}
	f.mu.Lock()
	var result any = true
	switch method {
	case "getUpdates":
		ups := []map[string]any{}
		if len(f.batches) > 0 {
			ups, f.batches = f.batches[0], f.batches[1:]
		}
		result = ups
	case "getMe":
		result = map[string]any{"id": 1, "is_bot": true, "username": "TelegraphTestBot"}
	case "sendMessage":
		result = map[string]any{"message_id": len(f.calls)}
	}
	f.mu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func (f *fakeBotAPI) sent() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, c := range f.calls {
		if c.method == "sendMessage" {
			out = append(out, c.req)
		}
	}
	return out
}

func (f *fakeBotAPI) methods(name string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, c := range f.calls {
		if c.method == name {
			out = append(out, c.req)
		}
	}
	return out
}

func msgUpdate(id int64, kind string, chat int64, chatType, text string) map[string]any {
	return map[string]any{"update_id": id, kind: map[string]any{
		"message_id": id, "text": text, "chat": map[string]any{"id": chat, "type": chatType}}}
}

func dm(id int64, chat int64, text string) map[string]any {
	return msgUpdate(id, "message", chat, "private", text)
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

func memStore() *store.Store {
	return &store.Store{Seen: map[string]bool{}, Tips: map[string]string{}, Peers: map[string]*store.Peer{}}
}

func testSyncer(t *testing.T, api *fakeBotAPI, st *store.Store) (*Syncer, *Bot, func()) {
	t.Helper()
	srv := httptest.NewServer(api)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	dieOnT(t, err)
	bot := NewBot(testToken)
	bot.BaseURL = srv.URL
	bot.sleep = func(time.Duration) {}
	sy := NewSyncer([]Conn{bot}, []string{"-100"}, st, priv)
	sy.DM = &Responder{Version: "v0.2.1"}
	return sy, bot, srv.Close
}

// forbidden in any reply: CLI and shell details have no place in Telegram.
var cliLeaks = []string{"export ", "TELEGRAPH_", "--pin", "--genesis", "go install", "$ ", "<pre>", "/dump"}

// A realistic first session, replayed through the real client against a
// fake Bot API: every reply is checked for content, format and escaping.
func TestBotSessionReplay(t *testing.T) {
	alice := signedEntry(t, "alice@nym")
	evil := signedEntry(t, "evil@nym")
	carol := signedEntry(t, "carol@nym")
	api := &fakeBotAPI{batches: [][]map[string]any{{
		dm(1, 11, "/start"),
		dm(2, 11, "/help@TelegraphTestBot"),
		dm(3, 11, "/RESOLVE alice@nym"),
		dm(4, 11, "/resolve"),
		dm(5, 11, "alice@nym"), // answers the "which handle?" prompt
	}, {
		dm(6, 12, "/resolve <b>x</b>@nym"),
		dm(7, 12, "/resolve "+strings.Repeat("a", 200)+"@nym"),
		dm(8, 12, "/resolve 🙂🙂"),
		dm(9, 12, "/pin"),
		dm(10, 13, "/status"),
		dm(11, 13, "/about"),
		dm(12, 13, "/version"),
		dm(13, 13, "/frobnicate<script>"),
	}, {
		dm(14, 14, "hello there"),
		dm(15, 14, "bob@nym"),
		dm(16, 14, oneFrame(t, evil)),    // a frame sent privately: never ingested
		dm(17, 14, "/help@SomeOtherBot"), // not for us: silent
		{"update_id": 18, "edited_message": map[string]any{"message_id": 1, "text": "/start",
			"chat": map[string]any{"id": 11, "type": "private"}}},
		{"update_id": 19, "my_chat_member": map[string]any{"chat": map[string]any{"id": 33, "type": "group"}}},
	}, {
		msgUpdate(20, "message", 33, "group", "/help"),                         // unaddressed: silent
		msgUpdate(21, "message", 33, "group", "hi all"),                        // chatter: silent
		msgUpdate(22, "message", 33, "group", oneFrame(t, evil)),               // gossip in a random group: ignored
		msgUpdate(23, "message", 33, "group", "/dump"),                         // dump request from a random group: ignored
		msgUpdate(24, "message", 33, "supergroup", "/status@telegraphtestbot"), // addressed: answered
		msgUpdate(25, "channel_post", -100, "channel", oneFrame(t, carol)),     // configured channel: ingested
		msgUpdate(26, "channel_post", -200, "channel", oneFrame(t, evil)),      // other channel: ignored
	}}}
	st := memStore()
	if !st.AppendReceived(alice) {
		t.Fatal("seed rejected")
	}
	st.Pin = "c700daaa0bac3c2256c32123b2eb43ccb07f67f25df53173c4e22622babd0080"
	sy, _, stop := testSyncer(t, api, st)
	defer stop()
	sy.SetupBots()
	for i := 0; i < 4; i++ {
		if _, err := sy.PollWindow(0); err != nil {
			t.Fatal(err)
		}
	}

	for _, e := range st.Entries {
		if e.Payload.Handle == "evil@nym" {
			t.Fatal("user or foreign-chat content entered the log")
		}
	}
	if len(st.Entries) != 2 {
		t.Fatalf("store has %d entries, want 2 (seed + configured channel post)", len(st.Entries))
	}
	for _, c := range api.methods("sendMessage") {
		if c["chat_id"] == "33" && c["text"] != nil && strings.HasPrefix(c["text"].(string), "⟁|") {
			t.Fatal("frames sent to a group")
		}
	}

	want := []struct {
		chat    string
		has     []string
		buttons bool
	}{
		{"11", []string{"<b>⟁ Telegraph</b>", "/resolve", "/help", "README"}, true},
		{"11", []string{"<b>Commands</b>", "/resolve <i>handle@nym</i>", "/status", "/pin", "/about", "/version", "/help", "/start"}, false},
		{"11", []string{"<b>alice@nym</b> · registered", "did:alice@nym", alice.KeyHex()[:8], "Not attested yet"}, false},
		{"11", []string{"Which handle?"}, false},
		{"11", []string{"<b>alice@nym</b>"}, false},
		{"12", []string{"&lt;b&gt;x&lt;/b&gt;@nym", "not registered"}, false},
		{"12", []string{"too long"}, false},
		{"12", []string{"🙂🙂", "is not a handle"}, false},
		{"12", []string{"<b>Trust pin</b>", "<code>c700daaa", "not a secret"}, false},
		{"13", []string{"v0.2.1", "Entries 1 · handles 1", "Checkpoint: none yet"}, false}, // before the channel post
		{"13", []string{"<b>About Telegraph</b>", "README"}, true},
		{"13", []string{"<b>Telegraph</b> v0.2.1"}, true},
		{"13", []string{"Unknown command <code>/frobnicate&lt;script&gt;</code>", "/help"}, false},
		{"14", []string{"Send /help"}, false},
		{"14", []string{"<b>bob@nym</b> is not registered"}, false},
		{"14", []string{"Send /help"}, false},
		{"33", []string{"Entries"}, false},
	}
	sent := api.sent()
	if len(sent) != len(want) {
		for i, m := range sent {
			t.Logf("reply %d to %v: %v", i, m["chat_id"], m["text"])
		}
		t.Fatalf("%d replies, want %d", len(sent), len(want))
	}
	for i, w := range want {
		m := sent[i]
		text, _ := m["text"].(string)
		if m["chat_id"] != w.chat || m["parse_mode"] != "HTML" {
			t.Fatalf("reply %d: chat %v parse_mode %v", i, m["chat_id"], m["parse_mode"])
		}
		for _, h := range w.has {
			if !strings.Contains(text, h) {
				t.Errorf("reply %d lacks %q:\n%s", i, h, text)
			}
		}
		for _, bad := range cliLeaks {
			if strings.Contains(text, bad) {
				t.Errorf("reply %d contains CLI detail %q:\n%s", i, bad, text)
			}
		}
		if strings.Contains(text, "<b>x</b>") || strings.Contains(text, "<script>") {
			t.Errorf("reply %d echoes unescaped input", i)
		}
		rm, _ := m["reply_markup"].(map[string]any)
		if _, has := rm["inline_keyboard"]; has != w.buttons {
			t.Errorf("reply %d: inline buttons %v, want %v", i, has, w.buttons)
		}
	}
	if fr, _ := sent[3]["reply_markup"].(map[string]any); fr["force_reply"] != true {
		t.Error("/resolve without a handle does not prompt for one")
	}
	if rp, _ := sent[16]["reply_parameters"].(map[string]any); rp["message_id"] != float64(24) {
		t.Error("group answer is not a reply to the command")
	}
	kb := sent[0]["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
	var labels []string
	for _, row := range kb {
		for _, b := range row.([]any) {
			labels = append(labels, b.(map[string]any)["text"].(string))
		}
	}
	if strings.Join(labels, ",") != "GitHub,Wiki,Channel,Release" {
		t.Errorf("start buttons = %v", labels)
	}
}

// Telegram remembers allowed_updates; every poll must send it.
func TestPollSendsAllowedUpdates(t *testing.T) {
	api := &fakeBotAPI{}
	sy, _, stop := testSyncer(t, api, memStore())
	defer stop()
	sy.PollWindow(0)
	polls := api.methods("getUpdates")
	if len(polls) == 0 {
		t.Fatal("no poll")
	}
	got, _ := json.Marshal(polls[0]["allowed_updates"])
	if string(got) != `["message","channel_post","my_chat_member"]` {
		t.Fatalf("allowed_updates = %s", got)
	}
	if polls[0]["timeout"] != float64(PollTimeout) {
		t.Fatalf("timeout = %v, want %d", polls[0]["timeout"], PollTimeout)
	}
}

// The menu comes from the same table as /help and never lists /dump.
func TestSetupMenuMatchesHelp(t *testing.T) {
	api := &fakeBotAPI{}
	sy, bot, stop := testSyncer(t, api, memStore())
	defer stop()
	sy.SetupBots()
	if bot.Username != "TelegraphTestBot" || sy.DM.BotUsername != "TelegraphTestBot" {
		t.Fatalf("username not learned: %q", bot.Username)
	}
	sets := api.methods("setMyCommands")
	if len(sets) != 2 {
		t.Fatalf("%d setMyCommands calls, want 2", len(sets))
	}
	help, _ := sy.DM.Handle(memStore(), Incoming{Chat: 1, Private: true, Text: "/help"})
	var scopes []string
	for _, s := range sets {
		scopes = append(scopes, s["scope"].(map[string]any)["type"].(string))
		cmds := s["commands"].([]any)
		if len(cmds) != len(Commands) {
			t.Fatalf("menu has %d commands, want %d", len(cmds), len(Commands))
		}
		for _, c := range cmds {
			name := c.(map[string]any)["command"].(string)
			desc := c.(map[string]any)["description"].(string)
			if name == "dump" || len(desc) < 3 || len(desc) > 256 {
				t.Errorf("bad menu entry %q %q", name, desc)
			}
			if !strings.Contains(help.HTML, "/"+name) || !strings.Contains(help.HTML, desc) {
				t.Errorf("/help lacks menu entry /%s — %s", name, desc)
			}
		}
	}
	if strings.Join(scopes, ",") != "default,all_private_chats" {
		t.Errorf("scopes = %v", scopes)
	}
	mb := api.methods("setChatMenuButton")
	if len(mb) != 1 || mb[0]["menu_button"].(map[string]any)["type"] != "commands" {
		t.Fatalf("menu button = %v", mb)
	}
}

func TestPollTimeoutBacksOffAndRedactsToken(t *testing.T) {
	old := RequestSlack
	RequestSlack = 200 * time.Millisecond
	defer func() { RequestSlack = old }()
	var hang atomic.Bool
	hang.Store(true)
	api := &fakeBotAPI{}
	api.hook = func(method string, _ map[string]any) (int, any, bool) {
		if method == "getUpdates" && hang.Load() {
			time.Sleep(time.Second)
		}
		return 0, nil, false
	}
	srv := httptest.NewServer(api)
	defer srv.Close()
	bot := NewBot(testToken)
	bot.BaseURL = srv.URL
	var slept []time.Duration
	bot.sleep = func(d time.Duration) { slept = append(slept, d) }
	for i := 0; i < 3; i++ {
		_, err := bot.Poll(0)
		if err == nil {
			t.Fatal("hung poll did not time out")
		}
		if strings.Contains(err.Error(), testToken) {
			t.Fatalf("error leaks the token: %v", err)
		}
	}
	if len(slept) != 3 || slept[0] < 500*time.Millisecond || slept[2] < 2*time.Second || slept[2] > 6*time.Second {
		t.Fatalf("backoff = %v, want ~1s, ~2s, ~4s with jitter", slept)
	}
	hang.Store(false)
	if _, err := bot.Poll(0); err != nil {
		t.Fatalf("poll after recovery: %v", err)
	}
	if bot.fails != 0 {
		t.Fatal("failure count not reset after success")
	}
}

func TestRetryAfterHonoured(t *testing.T) {
	api := &fakeBotAPI{}
	api.hook = func(method string, _ map[string]any) (int, any, bool) {
		return http.StatusTooManyRequests, map[string]any{"ok": false, "error_code": 429,
			"description": "Too Many Requests: retry after 7", "parameters": map[string]any{"retry_after": 7}}, true
	}
	srv := httptest.NewServer(api)
	defer srv.Close()
	bot := NewBot(testToken)
	bot.BaseURL = srv.URL
	var slept time.Duration
	bot.sleep = func(d time.Duration) { slept = d }
	_, err := bot.Poll(0)
	var ra *RetryAfterError
	if !errors.As(err, &ra) || slept < 7*time.Second {
		t.Fatalf("err=%v slept=%v, want RetryAfterError and >= 7s", err, slept)
	}
}

func TestUnreachableErrorRedactsToken(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	bot := NewBot(testToken)
	bot.BaseURL = url
	bot.sleep = func(time.Duration) {}
	_, err := bot.Poll(0)
	if err == nil || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), "/bot") {
		t.Fatalf("error = %v", err)
	}
}

func TestRateLimitNoticeOnce(t *testing.T) {
	now := time.Unix(1000, 0)
	r := &Responder{Limit: 2, Window: 10 * time.Second, Now: func() time.Time { return now }}
	st := memStore()
	in := Incoming{Chat: 1, Private: true, Text: "/pin"}
	for i := 0; i < 2; i++ {
		if _, ok := r.Handle(st, in); !ok {
			t.Fatalf("message %d dropped", i)
		}
	}
	rep, ok := r.Handle(st, in)
	if !ok || !strings.Contains(rep.HTML, "Too many messages") || !strings.Contains(rep.HTML, "10 s") {
		t.Fatalf("no rate-limit notice: %v %q", ok, rep.HTML)
	}
	if _, ok := r.Handle(st, in); ok {
		t.Fatal("second over-limit message answered")
	}
	if _, ok := r.Handle(st, Incoming{Chat: 2, Private: true, Text: "/pin"}); !ok {
		t.Fatal("limit leaked across chats")
	}
	now = now.Add(11 * time.Second)
	if _, ok := r.Handle(st, in); !ok {
		t.Fatal("limit did not reset after the window")
	}
}

func TestEmptyStoreReplies(t *testing.T) {
	r := &Responder{Version: "v0.2.1"}
	st := memStore()
	for cmd, want := range map[string]string{
		"/status":          "no entries yet",
		"/pin":             "No pin yet",
		"/resolve a@nym":   "not registered",
		"/version":         "v0.2.1",
		"/start@SomeBot x": "",
	} {
		rep, ok := r.Handle(st, Incoming{Chat: 5, Private: true, Text: cmd})
		if want == "" {
			continue
		}
		if !ok || !strings.Contains(rep.HTML, want) {
			t.Errorf("%s: %q, want %q", cmd, rep.HTML, want)
		}
	}
}

func TestReplyIgnoredWithoutResponder(t *testing.T) {
	api := &fakeBotAPI{batches: [][]map[string]any{{dm(1, 11, "/start")}}}
	sy, _, stop := testSyncer(t, api, memStore())
	defer stop()
	sy.DM = nil
	sy.PollWindow(0)
	if n := len(api.sent()); n != 0 {
		t.Fatalf("%d replies without a responder", n)
	}
}

func TestLogLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	var lines []string
	l := newLogLimiter(func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) })
	l.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		l.Printf("poll: %s", "timeout")
	}
	l.Printf("other")
	now = now.Add(61 * time.Second)
	l.Printf("poll: %s", "timeout")
	want := []string{"poll: timeout", "other", "poll: timeout (repeated 4 more times)"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %q", lines)
	}
}
