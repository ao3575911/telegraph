package transport

import (
	"fmt"
	"html"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ao3575911/telegraph/internal/checkpoint"
	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

// HTMLSender is implemented by connections that can send formatted
// replies (the Telegram Bot). The file bus does not answer DMs.
type HTMLSender interface {
	SendHTML(chat, text string) error
}

// Responder answers direct (private-chat) messages to the bot with
// read-only information from the local store. DM text is never parsed as
// a frame and never enters the gossip log.
type Responder struct {
	Version   string
	RepoURL   string
	WikiURL   string
	InviteURL string
	Channels  []string // shown in the join command

	// Limit replies per chat per Window; extra messages are dropped.
	Limit  int
	Window time.Duration
	Now    func() time.Time

	mu   sync.Mutex
	hits map[int64][]time.Time
}

// Defaults for the public network.
const (
	DefaultRepoURL   = "https://github.com/ao3575911/telegraph"
	DefaultWikiURL   = "https://github.com/ao3575911/telegraph/wiki"
	DefaultInviteURL = "https://t.me/+KEEgOfyWFC1iZGE9"
	maxHandleLen     = 64
)

func (r *Responder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// allow records a message from chat and reports whether it may be
// answered under the per-chat rate limit.
func (r *Responder) allow(chat int64) bool {
	limit, win := r.Limit, r.Window
	if limit <= 0 {
		limit = 5
	}
	if win <= 0 {
		win = 30 * time.Second
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hits == nil || len(r.hits) > 10000 {
		r.hits = map[int64][]time.Time{}
	}
	now := r.now()
	kept := r.hits[chat][:0]
	for _, t := range r.hits[chat] {
		if now.Sub(t) < win {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		r.hits[chat] = kept
		return false
	}
	r.hits[chat] = append(kept, now)
	return true
}

// Handle answers one private message. It returns the HTML reply and
// false when the message should be ignored (rate-limited or empty).
func (r *Responder) Handle(st *store.Store, chat int64, text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || !r.allow(chat) {
		return "", false
	}
	cmd, arg, _ := strings.Cut(text, " ")
	cmd = strings.ToLower(cmd)
	if at := strings.IndexByte(cmd, '@'); at > 0 {
		cmd = cmd[:at] // /start@telegraph_bot
	}
	arg = strings.TrimSpace(arg)
	switch cmd {
	case "/start":
		return r.start(st), true
	case "/help":
		return r.help(), true
	case "/resolve":
		return r.resolve(st, arg), true
	case "/pin":
		return r.pin(st), true
	case "/status":
		return r.status(st), true
	}
	return "Send /help for commands, or /resolve <code>handle@nym</code>.", true
}

func esc(s string) string { return html.EscapeString(s) }

func link(url, label string) string {
	return fmt.Sprintf(`<a href="%s">%s</a>`, esc(url), esc(label))
}

func (r *Responder) links() string {
	repo, wiki, inv := r.RepoURL, r.WikiURL, r.InviteURL
	if repo == "" {
		repo = DefaultRepoURL
	}
	if wiki == "" {
		wiki = DefaultWikiURL
	}
	if inv == "" {
		inv = DefaultInviteURL
	}
	return link(repo, "GitHub") + " · " + link(wiki, "Wiki") + " · " + link(inv, "Channel")
}

func (r *Responder) joinCmd(st *store.Store) string {
	chans := "&lt;channel&gt;"
	if len(r.Channels) > 0 {
		chans = esc(strings.Join(r.Channels, ","))
	}
	pin := "--genesis"
	if st.Pin != "" {
		pin = "--pin " + esc(st.Pin)
	}
	return "<pre>export TELEGRAPH_BOTS=&lt;your-bot-token&gt; TELEGRAPH_CHANNELS=" + chans +
		"\ntelegraph init\ntelegraph sync " + pin + "</pre>"
}

func (r *Responder) start(st *store.Store) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>⟁ Telegraph</b> %s\n", esc(r.Version))
	b.WriteString("A decentralised handle registry: signed, hash-chained logs gossiped over " +
		"Telegram and attested by rotating 2-of-3 checkpoint committees.\n\n")
	b.WriteString("<b>Install</b>\n<code>go install github.com/ao3575911/telegraph/cmd/telegraph@latest</code>\n\n")
	b.WriteString("<b>Join</b> (the pin is the network's trust root)\n")
	b.WriteString(r.joinCmd(st) + "\n")
	b.WriteString(r.links() + "\n\n")
	b.WriteString("/help quickstart · /resolve <code>handle@nym</code> · /pin · /status")
	return b.String()
}

func (r *Responder) help() string {
	return "<b>Quickstart</b> (three local nodes, no Telegram)\n<pre>" + esc(`go install github.com/ao3575911/telegraph/cmd/telegraph@latest
export TG=/tmp/tg TELEGRAPH_BUS=/tmp/tg/bus.jsonl; mkdir -p $TG
alice() { TELEGRAPH_HOME=$TG/alice telegraph "$@"; }
bob()   { TELEGRAPH_HOME=$TG/bob   telegraph "$@"; }
alice init && bob init
alice register --handle alice@nym --did did:key:alice
bob register --handle bob@nym --did did:key:bob
alice sync --secs 0; bob sync --secs 0; alice sync --secs 0
alice checkpoint; bob checkpoint
alice sync --secs 0; bob verify alice@nym`) + "</pre>\n" +
		"Full guide: " + link(DefaultRepoURL+"/blob/main/docs/QUICKSTART.md", "QUICKSTART") + "\n\n" +
		"/resolve <code>handle@nym</code> · /pin · /status"
}

func (r *Responder) resolve(st *store.Store, handle string) string {
	if handle == "" {
		return "Usage: /resolve <code>handle@nym</code>"
	}
	if utf8.RuneCountInString(handle) > maxHandleLen || strings.ContainsAny(handle, " \n\t") {
		return "That is not a handle. Usage: /resolve <code>handle@nym</code>"
	}
	reg, err := st.Registry()
	if err != nil {
		return "⚠️ Local log failed verification; try again later."
	}
	e, status, ok := reg.Resolve(handle)
	if !ok {
		return fmt.Sprintf("<b>%s</b> is not registered.", esc(handle))
	}
	if e.Payload.Kind == tlog.KindRevoke {
		return fmt.Sprintf("<b>%s</b> is revoked.", esc(handle))
	}
	did := "<i>none yet (new owner has not registered one)</i>"
	if e.Payload.Kind == tlog.KindRegister {
		did = "<code>" + esc(e.Payload.DID) + "</code>"
	}
	rep := checkpoint.Audit(st.Entries)
	var att string
	switch {
	case rep.Diverged:
		att = "⚠️ DIVERGED: local log disagrees with its checkpoint"
	case rep.NoCheckpoint:
		att = "UNATTESTED: no checkpoint yet"
	case rep.CoveredHashes[e.HashHex()]:
		att = "✅ ATTESTED by checkpoint <code>" + short8(rep.CP.HashHex()) + "</code>"
	default:
		att = "UNCOVERED: newer than checkpoint <code>" + short8(rep.CP.HashHex()) + "</code>"
	}
	return fmt.Sprintf("<b>%s</b>  [%s]\nowner  <code>%s</code>\nDID  %s\nsince  %s\n%s",
		esc(handle), status.String(), short8(e.KeyHex()), did,
		time.Unix(e.Payload.TS, 0).UTC().Format("2006-01-02 15:04 UTC"), att)
}

func (r *Responder) pin(st *store.Store) string {
	if st.Pin == "" {
		return "This node has no pin yet: no checkpoint has been pinned."
	}
	return "<b>Trust pin</b>\n<code>" + esc(st.Pin) + "</code>\nPass it to your first sync: " +
		"<code>telegraph sync --pin " + esc(st.Pin) + "</code>"
}

func (r *Responder) status(st *store.Store) string {
	rep := checkpoint.Audit(st.Entries)
	var b strings.Builder
	fmt.Fprintf(&b, "<b>⟁ Telegraph</b> %s\nentries  %d (%d issuers)\n",
		esc(r.Version), len(st.Entries), len(checkpoint.Issuers(st.Entries)))
	if rep.NoCheckpoint {
		b.WriteString("checkpoint  none yet")
		return b.String()
	}
	cp := rep.CP
	need := 1
	if len(cp.Payload.Comm) > 1 {
		need = tlog.RequiredCPSigs
	}
	state := "✅ audit clean"
	if rep.Diverged {
		state = "⚠️ audit diverged"
	}
	fmt.Fprintf(&b, "checkpoint  <code>%s</code> · %d entries · %s\ncommittee  %d members · %d of %d signatures\n%s",
		short8(cp.HashHex()), cp.Payload.Count,
		time.Unix(cp.Payload.TS, 0).UTC().Format("2006-01-02 15:04 UTC"),
		len(cp.Payload.Comm), 1+len(cp.CoSigs), need, state)
	if rep.PendingCP > 0 {
		fmt.Fprintf(&b, "\n%d checkpoint awaiting co-signature", rep.PendingCP)
	}
	return b.String()
}

func short8(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
