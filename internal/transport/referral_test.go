package transport

import (
	"strings"
	"testing"
)

// A node without bot registration that names a RegisterBot sends users
// there for /register and /card, in replies, menu and descriptions.
func TestRegisterBotReferral(t *testing.T) {
	r, st := &Responder{Version: "v0.3.1", BotUsername: "node2bot", RegisterBot: "dsmdioefncoedcbot", Limit: 1 << 20}, memStore()
	for _, cmd := range []string{"/register adz", "/register", "/revoke adz", "/card"} {
		rep, ok := r.Handle(st, Incoming{Chat: 42, User: 42, Private: true, Text: cmd})
		if !ok || !strings.Contains(rep.HTML, "@dsmdioefncoedcbot") {
			t.Errorf("%s: no referral: %q", cmd, rep.HTML)
		}
		if len(rep.Buttons) != 1 || rep.Buttons[0][0].URL != "https://t.me/dsmdioefncoedcbot" {
			t.Errorf("%s: buttons %+v", cmd, rep.Buttons)
		}
		if rep.ForceReply != "" {
			t.Errorf("%s: prompted for a name on a non-registering node", cmd)
		}
	}
	// In a group the referral wins over "DM me": this node can't register anyway.
	rep, _ := r.Handle(st, Incoming{Chat: -5, User: 42, Text: "/register@node2bot adz", ID: 7})
	if !strings.Contains(rep.HTML, "@dsmdioefncoedcbot") {
		t.Errorf("group /register: %q", rep.HTML)
	}
	if got := send(t, r, st, 42, "/resolve free@nym"); !strings.Contains(got, "to @dsmdioefncoedcbot") {
		t.Errorf("unknown handle: %q", got)
	}
	if len(st.Entries) != 0 {
		t.Fatalf("referral node wrote %d entries", len(st.Entries))
	}

	menu := map[string]string{}
	for _, c := range r.Menu() {
		menu[c.Command] = c.Description
		if len(c.Description) < 3 || len(c.Description) > 256 {
			t.Errorf("menu /%s description %d chars", c.Command, len(c.Description))
		}
	}
	if !strings.Contains(menu["register"], "@dsmdioefncoedcbot") || !strings.Contains(menu["card"], "@dsmdioefncoedcbot") {
		t.Errorf("menu lacks referral: %v", menu)
	}
	if _, ok := menu["revoke"]; ok {
		t.Error("menu lists /revoke on a non-registering node")
	}
	help := send(t, r, st, 42, "/help")
	for _, c := range r.Menu() {
		if !strings.Contains(help, "/"+c.Command) || !strings.Contains(help, esc(c.Description)) {
			t.Errorf("/help lacks /%s — %s", c.Command, c.Description)
		}
	}
	if start := send(t, r, st, 42, "/start"); !strings.Contains(start, "@dsmdioefncoedcbot") {
		t.Errorf("/start: %q", start)
	}

	short, long := r.Descriptions()
	if !strings.Contains(short, "@dsmdioefncoedcbot") || !strings.Contains(long, "@dsmdioefncoedcbot") {
		t.Errorf("descriptions lack referral:\n%s\n%s", short, long)
	}
	long32 := &Responder{Version: "v0.3.1", RegisterBot: strings.Repeat("a", 29) + "bot"}
	if s, l := long32.Descriptions(); len([]rune(s)) > 120 || len([]rune(l)) > 512 {
		t.Errorf("32-char RegisterBot overflows: short %d, long %d", len([]rune(s)), len([]rune(l)))
	}
}

// RegisterBot is ignored by a node that holds handles itself, and a node
// with neither keeps the generic CLI pointer.
func TestRegisterBotIgnoredWhenRegistering(t *testing.T) {
	r := regResponder(t)
	r.RegisterBot = "otherbot"
	if got := send(t, r, memStore(), 42, "/register adz"); strings.Contains(got, "@otherbot") {
		t.Errorf("registering node referred away: %q", got)
	}
	if len(r.Menu()) != len(MenuCommands(true)) {
		t.Error("registering node menu changed")
	}
	plain := &Responder{}
	if len(plain.Menu()) != len(MenuCommands(false)) {
		t.Error("plain node menu changed")
	}
	if got := send(t, plain, memStore(), 42, "/card"); !strings.Contains(got, "telegraph card") {
		t.Errorf("plain /card: %q", got)
	}
}

func TestValidBotUsername(t *testing.T) {
	for u, want := range map[string]bool{
		"dsmdioefncoedcbot": true, "Telegraph_Bot": true, "abcbot": true,
		"bot": false, "abot": false, "1abcbot": false, "_abcbot": false,
		"abc-bot": false, "abcdef": false, "@abcbot": false,
		strings.Repeat("a", 29) + "bot": true, strings.Repeat("a", 30) + "bot": false,
	} {
		if got := ValidBotUsername(u); got != want {
			t.Errorf("ValidBotUsername(%q) = %v, want %v", u, got, want)
		}
	}
}
