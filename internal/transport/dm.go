package transport

import (
	"crypto/ed25519"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ao3575911/telegraph/internal/checkpoint"
	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

// Replier is implemented by connections that can send formatted replies
// (the Telegram Bot). The file bus does not answer users.
type Replier interface {
	SendReply(chat string, r Reply) error
}

// Command is one bot command. The same table drives /help and the
// Telegram command menu (setMyCommands), so they cannot drift apart.
type Command struct {
	Name  string // without the slash
	Args  string // shown in /help, e.g. "handle@nym"
	Short string // menu description (Telegram allows 3-256 chars)
}

// Commands lists every command users can send, in menu order. /dump is a
// node-to-node protocol message on gossip channels and is not listed.
var Commands = []Command{
	{"resolve", "handle@nym", "Look up a handle: owner, DID, attestation"},
	{"register", "name [did]", "Register name@nym, signed by this node for you"},
	{"revoke", "name", "Revoke a handle you registered here"},
	{"status", "", "Node and network status"},
	{"pin", "", "The network's trust pin"},
	{"about", "", "What Telegraph is and how it works"},
	{"version", "", "Node software version"},
	{"help", "", "List commands"},
	{"start", "", "Welcome"},
}

// MenuCommands is Commands in setMyCommands form.
func MenuCommands() []BotCommand {
	out := make([]BotCommand, len(Commands))
	for i, c := range Commands {
		out[i] = BotCommand{Command: c.Name, Description: c.Short}
	}
	return out
}

// Public links shown as inline buttons.
const (
	DefaultRepoURL    = "https://github.com/ao3575911/telegraph"
	DefaultWikiURL    = "https://github.com/ao3575911/telegraph/wiki"
	DefaultInviteURL  = "https://t.me/+KEEgOfyWFC1iZGE9"
	DefaultReleaseURL = "https://github.com/ao3575911/telegraph/releases/latest"
	DefaultNodeURL    = "https://github.com/ao3575911/telegraph#install"
	maxHandleLen      = tlog.MaxHandleLen
	pendingFor        = 5 * time.Minute
)

// Responder answers users from the local store. Text users send is never
// parsed as a frame. The only entries it writes are /register and /revoke,
// signed with Priv (the node's own key) when Priv is set; see register.go.
type Responder struct {
	Version     string
	BotUsername string // without @; groups are answered only when addressed to it

	// Priv enables /register and /revoke: the node signs handles for
	// Telegram users (custodial). nil turns both commands off.
	Priv ed25519.PrivateKey

	// Limit replies per chat per Window. The first message over the limit
	// gets a short notice; later ones are dropped until the window clears.
	Limit  int
	Window time.Duration
	Now    func() time.Time

	mu       sync.Mutex
	hits     map[int64][]time.Time
	notified map[int64]bool
	pending  map[int64]ask // chats asked a question, awaiting the answer
}

// ask is an open question to a chat: which command the answer is for.
type ask struct {
	cmd string
	at  time.Time
}

func (r *Responder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// allow records a message and reports whether to answer it; notice is
// set for the first message over the limit.
func (r *Responder) allow(chat int64) (ok bool, notice time.Duration) {
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
		r.hits, r.notified, r.pending = map[int64][]time.Time{}, map[int64]bool{}, map[int64]ask{}
	}
	now := r.now()
	kept := r.hits[chat][:0]
	for _, t := range r.hits[chat] {
		if now.Sub(t) < win {
			kept = append(kept, t)
		}
	}
	r.hits[chat] = kept
	if len(kept) >= limit {
		if r.notified[chat] {
			return false, 0
		}
		r.notified[chat] = true
		wait := win - now.Sub(kept[0])
		return false, (wait + time.Second - 1).Truncate(time.Second) // round up
	}
	r.notified[chat] = false
	r.hits[chat] = append(kept, now)
	return true, 0
}

// Incoming is one user message as the responder sees it.
type Incoming struct {
	Chat    int64
	Private bool // false: group or supergroup
	Text    string
	ID      int64 // message id, for replies in groups
	User    int64 // sender's Telegram user id; 0 when unknown
}

// Handle answers one message. ok is false when the bot should stay
// silent (group chatter, rate-limited, empty).
func (r *Responder) Handle(st *store.Store, in Incoming) (Reply, bool) {
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return Reply{}, false
	}
	cmd, arg, isCmd := r.parse(text)
	if !in.Private {
		// In groups, answer only commands explicitly addressed to this bot.
		if !isCmd || !addressedTo(text, r.BotUsername) {
			return Reply{}, false
		}
	} else if isCmd && !addressedOK(text, r.BotUsername) {
		return Reply{}, false // /cmd@otherbot
	}
	ok, wait := r.allow(in.Chat)
	if !ok {
		if wait > 0 {
			return r.reply(in, fmt.Sprintf("Too many messages. Try again in %d s.", int(wait.Seconds())), nil), true
		}
		return Reply{}, false
	}
	if !isCmd {
		if in.Private {
			switch r.takePending(in.Chat) {
			case "resolve":
				return r.resolve(st, in, text), true
			case "register":
				return r.register(st, in, text), true
			}
		}
		if in.Private && looksLikeHandle(text) {
			return r.resolve(st, in, text), true
		}
		return r.reply(in, "Send /help to see what I can do, or a handle like <code>alice@nym</code> to look it up.", nil), true
	}
	switch cmd {
	case "start":
		return r.start(in), true
	case "help":
		return r.help(in), true
	case "resolve":
		if arg == "" {
			if !in.Private {
				return r.reply(in, "Usage: <code>/resolve handle@nym</code>", nil), true
			}
			r.setPending(in.Chat, "resolve")
			rep := r.reply(in, "Which handle? Send it as a reply, for example <code>alice@nym</code>.", nil)
			rep.ForceReply = "alice@nym"
			return rep, true
		}
		return r.resolve(st, in, arg), true
	case "register":
		if in.Private && arg == "" && r.Priv != nil {
			r.setPending(in.Chat, "register")
			rep := r.reply(in, "Which name? Send it as a reply, for example <code>adz</code> "+
				"(or <code>adz did:key:…</code> to set your own DID).", nil)
			rep.ForceReply = "adz"
			return rep, true
		}
		return r.register(st, in, arg), true
	case "revoke":
		return r.revoke(st, in, arg), true
	case "pin":
		return r.pin(st, in), true
	case "status":
		return r.status(st, in), true
	case "about":
		return r.about(in), true
	case "version":
		return r.version(in), true
	}
	return r.reply(in, fmt.Sprintf("Unknown command <code>/%s</code>. Send /help for the list.", esc(clip(cmd, 32))), nil), true
}

// parse splits "/Cmd@bot arg" into ("cmd", "arg", true).
func (r *Responder) parse(text string) (cmd, arg string, isCmd bool) {
	if !strings.HasPrefix(text, "/") || len(text) < 2 {
		return "", "", false
	}
	head, rest, _ := strings.Cut(text, " ")
	if i := strings.IndexAny(head, "\n\t"); i >= 0 {
		rest = head[i+1:] + " " + rest
		head = head[:i]
	}
	head = strings.TrimPrefix(head, "/")
	if at := strings.IndexByte(head, '@'); at >= 0 {
		head = head[:at]
	}
	return strings.ToLower(head), strings.TrimSpace(rest), true
}

func botSuffix(text string) string {
	head, _, _ := strings.Cut(text, " ")
	if at := strings.IndexByte(head, '@'); at >= 0 {
		return head[at+1:]
	}
	return ""
}

func addressedTo(text, bot string) bool {
	return bot != "" && strings.EqualFold(botSuffix(text), bot)
}

func addressedOK(text, bot string) bool {
	s := botSuffix(text)
	return s == "" || bot == "" || strings.EqualFold(s, bot)
}

func looksLikeHandle(s string) bool { return tlog.ValidHandle(s) == nil }

func (r *Responder) setPending(chat int64, cmd string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending == nil {
		r.pending = map[int64]ask{}
	}
	r.pending[chat] = ask{cmd, r.now()}
}

// takePending returns the command an open question in chat is for, or ""
// when there is none (or it expired).
func (r *Responder) takePending(chat int64) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.pending[chat]
	delete(r.pending, chat)
	if !ok || r.now().Sub(a.at) >= pendingFor {
		return ""
	}
	return a.cmd
}

// esc escapes text for Telegram's HTML parse mode, which needs only
// &, < and > (and " inside attributes) escaped.
var htmlEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func esc(s string) string { return htmlEsc.Replace(s) }

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func (r *Responder) reply(in Incoming, text string, buttons [][]Button) Reply {
	rep := Reply{HTML: text, Buttons: buttons}
	if !in.Private {
		rep.ReplyTo = in.ID
	}
	return rep
}

func linkButtons() [][]Button {
	return [][]Button{
		{{Text: "GitHub", URL: DefaultRepoURL}, {Text: "Wiki", URL: DefaultWikiURL}},
		{{Text: "Channel", URL: DefaultInviteURL}, {Text: "Release", URL: DefaultReleaseURL}},
	}
}

func (r *Responder) ver() string {
	if r.Version == "" {
		return "dev"
	}
	return r.Version
}

func (r *Responder) start(in Incoming) Reply {
	return r.reply(in, "<b>⟁ Telegraph</b>\n"+
		"A decentralised handle registry. Handles like <code>alice@nym</code> live in signed logs "+
		"that nodes gossip over Telegram and checkpoint together.\n\n"+
		"/resolve — look up a handle\n"+
		"/register — claim a handle like <code>adz@nym</code>\n"+
		"/status — this node and the network\n"+
		"/help — every command\n\n"+
		"Run your own node: <a href=\""+DefaultNodeURL+"\">README</a>", linkButtons())
}

func (r *Responder) help(in Incoming) Reply {
	var b strings.Builder
	b.WriteString("<b>Commands</b>\n")
	for _, c := range Commands {
		b.WriteString("/" + c.Name)
		if c.Args != "" {
			b.WriteString(" <i>" + esc(c.Args) + "</i>")
		}
		b.WriteString(" — " + esc(c.Short) + "\n")
	}
	b.WriteString("\nYou can also send a handle on its own, like <code>alice@nym</code>.")
	return r.reply(in, b.String(), nil)
}

func (r *Responder) about(in Incoming) Reply {
	return r.reply(in, "<b>About Telegraph</b>\n"+
		"• Each node keeps a signed, hash-chained log of handle claims.\n"+
		"• Nodes gossip entries over Telegram channels and merge them the same way, so they agree.\n"+
		"• A rotating committee of three nodes signs checkpoints; two signatures make one valid.\n"+
		"• New nodes trust only history that leads back to a published pin (/pin).\n"+
		"• Handles can be swapped atomically between owners.\n\n"+
		"No blockchain and no central server. Run your own node: <a href=\""+DefaultNodeURL+"\">README</a>",
		linkButtons())
}

func (r *Responder) version(in Incoming) Reply {
	return r.reply(in, "<b>Telegraph</b> "+esc(r.ver()),
		[][]Button{{{Text: "Releases", URL: DefaultReleaseURL}}})
}

func (r *Responder) resolve(st *store.Store, in Incoming, handle string) Reply {
	switch {
	case utf8.RuneCountInString(handle) > maxHandleLen:
		return r.reply(in, "That is too long for a handle (64 characters at most).", nil)
	case !looksLikeHandle(handle):
		return r.reply(in, "<code>"+esc(clip(handle, 64))+"</code> is not a handle. Handles look like <code>alice@nym</code>.", nil)
	}
	reg, err := st.Registry()
	if err != nil {
		return r.reply(in, "⚠️ This node's log failed verification. Try again later.", nil)
	}
	e, status, ok := reg.Resolve(handle)
	if !ok {
		return r.reply(in, "<b>"+esc(handle)+"</b> is not registered.", nil)
	}
	if e.Payload.Kind == tlog.KindRevoke {
		return r.reply(in, "<b>"+esc(handle)+"</b> was revoked by its owner.", nil)
	}
	did := "<i>none yet (new owner has not set one)</i>"
	if e.Payload.Kind == tlog.KindRegister {
		did = "<code>" + esc(clip(e.Payload.DID, 200)) + "</code>"
	}
	rep := checkpoint.Audit(st.Entries)
	var att string
	switch {
	case rep.Diverged:
		att = "⚠️ This node's log disagrees with its checkpoint"
	case rep.NoCheckpoint:
		att = "Not attested yet: no checkpoint"
	case rep.CoveredHashes[e.HashHex()]:
		att = "✅ Attested by checkpoint <code>" + short8(rep.CP.HashHex()) + "</code>"
	default:
		att = "⏳ Newer than the latest checkpoint"
	}
	return r.reply(in, fmt.Sprintf("<b>%s</b> · %s\nOwner <code>%s</code>\nDID %s\nSince %s\n%s",
		esc(handle), strings.ToLower(status.String()), short8(e.KeyHex()), did,
		time.Unix(e.Payload.TS, 0).UTC().Format("2 Jan 2006 15:04 UTC"), att), nil)
}

func (r *Responder) pin(st *store.Store, in Incoming) Reply {
	if st.Pin == "" {
		return r.reply(in, "No pin yet: this node has not pinned a checkpoint.", nil)
	}
	return r.reply(in, "<b>Trust pin</b>\n<code>"+esc(st.Pin)+"</code>\n"+
		"New nodes accept only history that leads back to this checkpoint. "+
		"It is a fingerprint, not a secret: share it freely.", nil)
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

func (r *Responder) status(st *store.Store, in Incoming) Reply {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>⟁ Telegraph</b> %s\n", esc(r.ver()))
	if len(st.Entries) == 0 {
		b.WriteString("This node has no entries yet.")
		return r.reply(in, b.String(), nil)
	}
	reg, _ := st.Registry()
	handles := 0
	if reg != nil {
		handles = len(reg.Head)
	}
	fmt.Fprintf(&b, "Entries %d · handles %d · issuers %d\n",
		len(st.Entries), handles, len(checkpoint.Issuers(st.Entries)))
	rep := checkpoint.Audit(st.Entries)
	if rep.NoCheckpoint {
		b.WriteString("Checkpoint: none yet")
		return r.reply(in, b.String(), nil)
	}
	cp := rep.CP
	need := 1
	if len(cp.Payload.Comm) > 1 {
		need = tlog.RequiredCPSigs
	}
	state := "✅ audit clean"
	if rep.Diverged {
		state = "⚠️ audit failed"
	}
	fmt.Fprintf(&b, "Checkpoint <code>%s</code> · %d entries · %s\nCommittee %d · signatures %d of %d\n%s",
		short8(cp.HashHex()), cp.Payload.Count, ago(r.now().Sub(time.Unix(cp.Payload.TS, 0))),
		len(cp.Payload.Comm), 1+len(cp.CoSigs), need, state)
	if rep.PendingCP > 0 {
		fmt.Fprintf(&b, "\n%d checkpoint awaiting co-signature", rep.PendingCP)
	}
	return r.reply(in, b.String(), nil)
}

func short8(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
