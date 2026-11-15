package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/tlog"
)

// flakyConn fails every Send while down, then records what it gets.
type flakyConn struct {
	down bool
	got  []string
}

func (c *flakyConn) Send(_, text string) error {
	if c.down {
		return &RetryAfterError{"sendMessage", time.Second}
	}
	c.got = append(c.got, text)
	return nil
}
func (c *flakyConn) Poll(int) ([]Update, error) { return nil, nil }

func TestPublishPendingKeepsUndeliveredEntries(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	dieOnT(t, err)
	st := memStore()
	e, err := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: "a@nym", DID: "did:key:a", TS: time.Now().Unix()}, priv, nil)
	dieOnT(t, err)
	st.AppendEntry(e)
	c := &flakyConn{down: true}
	sy := NewSyncer([]Conn{c}, []string{"@ch"}, st, priv)
	if _, err := sy.PublishPending(); err == nil {
		t.Fatal("publish with every send failing reported success")
	}
	if len(st.Pending()) != 1 {
		t.Fatal("undelivered entry was marked seen: it would never reach the network")
	}
	c.down = false
	if n, err := sy.PublishPending(); err != nil || n != 1 || len(c.got) != 1 || len(st.Pending()) != 0 {
		t.Fatalf("retry: n=%d err=%v frames=%d pending=%d", n, err, len(c.got), len(st.Pending()))
	}
}

func TestLimiterFloodDoesNotResetLimits(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := &Responder{Version: "t", Now: func() time.Time { return now }}
	st := memStore()
	answered := func(chat int64) bool {
		rep, ok := r.Handle(st, Incoming{Chat: chat, User: chat, Private: true, Text: "/version"})
		return ok && !strings.Contains(rep.HTML, "Too many")
	}
	for i := 0; i < 6; i++ {
		answered(1)
	}
	for i := 0; i < limiterChats+5; i++ {
		answered(int64(1000 + i))
	}
	if answered(1) {
		t.Fatal("a flood of new chats reset the victim's rate limit")
	}
	now = now.Add(31 * time.Second) // windows pass: room for new chats again
	if !answered(999_999) {
		t.Fatal("new chat refused after every window expired")
	}
}
