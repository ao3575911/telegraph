package transport

import (
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ao3575911/telegraph/internal/checkpoint"
	"github.com/ao3575911/telegraph/internal/crdt"
	"github.com/ao3575911/telegraph/internal/handshake"
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
	{"register", "name [did]", "Claim name@nym (held by this node for you)"},
	{"card", "[name]", "Your bio line, tap to copy"},
	{"resolve", "handle@nym", "Look up a handle"},
	{"revoke", "name", "Release a handle you hold here"},
	{"stats", "", "Network counts and badges"},
	{"status", "", "Node and checkpoint health"},
	{"pin", "", "The network trust pin"},
	{"about", "", "How Telegraph works"},
	{"version", "", "Node software version"},
	{"help", "", "All commands"},
	{"start", "", "Welcome and links"},
}

// custodialOnly reports whether command needs bot registration on.
func custodialOnly(name string) bool {
	return name == "register" || name == "revoke" || name == "card"
}

// MenuCommands is Commands in setMyCommands form. Without bot
// registration, /register, /revoke and /card are left out.
func MenuCommands(register bool) []BotCommand {
	var out []BotCommand
	for _, c := range Commands {
		if !register && custodialOnly(c.Name) {
			continue
		}
		out = append(out, BotCommand{Command: c.Name, Description: c.Short})
	}
	return out
}

// Menu is this responder's command menu. A node that holds handles lists
// every command. A node that does not, but names a RegisterBot, keeps
// /register and /card in the menu, described as pointers to that bot.
func (r *Responder) Menu() []BotCommand {
	if r.Priv != nil {
		return MenuCommands(true)
	}
	ref := r.referral()
	if ref == "" {
		return MenuCommands(false)
	}
	var out []BotCommand
	for _, c := range Commands {
		switch c.Name {
		case "register":
			out = append(out, BotCommand{Command: c.Name, Description: "Claim name@nym on " + ref})
		case "card":
			out = append(out, BotCommand{Command: c.Name, Description: "Your bio line, on " + ref})
		case "revoke":
		default:
			out = append(out, BotCommand{Command: c.Name, Description: c.Short})
		}
	}
	return out
}

// referral is "@RegisterBot", or "" when this node holds handles itself
// or names no other bot.
func (r *Responder) referral() string {
	if r.Priv != nil || r.RegisterBot == "" {
		return ""
	}
	return "@" + r.RegisterBot
}

// referralButtons links to the RegisterBot, or nil.
func (r *Responder) referralButtons() [][]Button {
	if r.referral() == "" {
		return nil
	}
	return [][]Button{{{Text: "Open " + r.referral(), URL: "https://t.me/" + r.RegisterBot}}}
}

// ValidBotUsername reports whether u (without @) is a Telegram bot
// username: 5-32 letters, digits or underscores, starting with a letter
// and ending in "bot".
func ValidBotUsername(u string) bool {
	if len(u) < 5 || len(u) > 32 || !strings.HasSuffix(strings.ToLower(u), "bot") {
		return false
	}
	for i, c := range u {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '_'):
		default:
			return false
		}
	}
	return true
}

// Descriptions returns the bot's profile texts: the short description
// (profile page and share preview, at most 120 characters) and the
// description shown in an empty chat (at most 512).
func (r *Responder) Descriptions() (short, long string) {
	ch := strings.TrimPrefix(r.channelURL(), "https://t.me/")
	short = "Decentralised handles like adz@nym, signed and gossiped over Telegram."
	var b strings.Builder
	fmt.Fprintf(&b, "⟁ Telegraph %s: a decentralised handle registry.\n\n", r.ver())
	if r.Priv != nil {
		short += " /register to claim yours."
		b.WriteString("/register name: claim name@nym\n/card: your bio line, tap to copy\n")
	} else if ref := r.referral(); ref != "" {
		short += " Claim yours: " + ref + "."
		fmt.Fprintf(&b, "This node verifies and gossips the registry. To claim name@nym, open %s and send /register name.\n\n", ref)
	}
	b.WriteString("/resolve handle: look anyone up\n/help: all commands\n\n")
	fmt.Fprintf(&b, "Entries are signed, gossiped on @%s and checkpointed by a 2-of-3 node committee. "+
		"Open source: github.com/ao3575911/telegraph", ch)
	return short, b.String()
}

// Public links shown as inline buttons.
// The channel link is a public t.me/<username> URL, never an invite link;
// operators override it with --channel-url or TELEGRAPH_CHANNEL_URL.
const (
	DefaultRepoURL    = "https://github.com/ao3575911/telegraph"
	DefaultWikiURL    = "https://github.com/ao3575911/telegraph/wiki"
	DefaultChannelURL = "https://t.me/tgraph_net"
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
	ChannelURL  string // public channel link for buttons; "" means DefaultChannelURL

	// RegisterBot (username without @) is the bot users are sent to for
	// /register and /card when this node does not hold handles (Priv nil).
	RegisterBot string

	// Log receives internal errors (never shown to users); nil means
	// slog.Default().
	Log *slog.Logger

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
	pruned   time.Time     // last limiter prune

	// auditKey/auditRep memoise checkpoint.Audit between log changes.
	auditKey [3]int
	auditSt  *store.Store
	auditRep checkpoint.Report
}

// lookupHandle resolves one handle through the store's per-handle index:
// the same answer as st.Registry().Resolve(handle) without scanning or
// re-verifying the whole log. handshake.Owner verifies every entry it
// folds for the handle, which is all a lookup needs, so one bad entry
// elsewhere cannot break every /resolve.
func lookupHandle(st *store.Store, handle string) (tlog.Entry, handshake.Status, bool) {
	return crdt.ResolveIn(handle, st.HandleEntries(handle), st.Entries)
}

// audit returns checkpoint.Audit for st, recomputed only when the log
// grew or a checkpoint gained co-signatures. Audit is O(log) and used to
// run on every /resolve and /status.
func (r *Responder) audit(st *store.Store) checkpoint.Report {
	cos, cps := 0, 0
	for i := range st.Entries {
		if st.Entries[i].Payload.Kind == tlog.KindCheckpoint {
			cps++
			cos += len(st.Entries[i].CoSigs)
		}
	}
	key := [3]int{len(st.Entries), cps, cos}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.auditSt != st || r.auditKey != key {
		r.auditRep, r.auditKey, r.auditSt = checkpoint.Audit(st.Entries), key, st
	}
	return r.auditRep
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
	if r.hits == nil {
		r.hits, r.notified = map[int64][]time.Time{}, map[int64]bool{}
	}
	now := r.now()
	if _, known := r.hits[chat]; !known && len(r.hits) >= limiterChats {
		// Prune only chats whose window has passed. Clearing everything,
		// as before v0.3.0, let a flood of new chats reset every limit.
		if now.Sub(r.pruned) >= time.Second { // a flood must not make every message O(chats)
			r.prune(now, win)
			r.pruned = now
		}
		if len(r.hits) >= limiterChats {
			return false, 0 // flooded with live chats: refuse newcomers
		}
	}
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

// limiterChats bounds the chats the limiter tracks at once.
const limiterChats = 10000

// prune drops chats with no message inside win, and expired questions.
// Caller holds mu.
func (r *Responder) prune(now time.Time, win time.Duration) {
	for c, ts := range r.hits {
		if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= win {
			delete(r.hits, c)
			delete(r.notified, c)
		}
	}
	for c, a := range r.pending {
		if now.Sub(a.at) >= pendingFor {
			delete(r.pending, c)
		}
	}
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
			return r.rateLimited(in, wait), true
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
		return r.reply(in, "Send a handle like <code>alice@nym</code> to look it up, or /help for commands.", nil), true
	}
	switch cmd {
	case "start":
		return r.start(in), true
	case "help":
		return r.help(in), true
	case "resolve":
		if arg == "" {
			if !in.Private {
				return r.usage(in, "/resolve handle@nym"), true
			}
			r.setPending(in.Chat, "resolve")
			rep := r.reply(in, "Which handle? Reply with one, for example <code>alice@nym</code>.", nil)
			rep.ForceReply = "alice@nym"
			return rep, true
		}
		return r.resolve(st, in, arg), true
	case "register":
		if in.Private && arg == "" && r.Priv != nil {
			r.setPending(in.Chat, "register")
			rep := r.reply(in, "Which name? Reply with one, for example <code>adz</code> "+
				"(or <code>adz did:key:…</code> to set your own DID).", nil)
			rep.ForceReply = "adz"
			return rep, true
		}
		return r.register(st, in, arg), true
	case "revoke":
		return r.revoke(st, in, arg), true
	case "card":
		return r.card(st, in, arg), true
	case "pin":
		return r.pin(st, in), true
	case "status":
		return r.status(st, in), true
	case "stats":
		return r.stats(st, in), true
	case "about":
		return r.about(in), true
	case "version":
		return r.version(in), true
	}
	return r.reply(in, fmt.Sprintf("⚠️ Unknown command <code>/%s</code>. Send /help for the list.", esc(clip(cmd, 32))), nil), true
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

func (r *Responder) channelURL() string {
	if r.ChannelURL != "" {
		return r.ChannelURL
	}
	return DefaultChannelURL
}

func (r *Responder) linkButtons() [][]Button {
	return [][]Button{
		{{Text: "📣 Channel", URL: r.channelURL()}, {Text: "GitHub", URL: DefaultRepoURL}},
		{{Text: "Wiki", URL: DefaultWikiURL}, {Text: "Releases", URL: DefaultReleaseURL}},
	}
}

func (r *Responder) ver() string {
	if r.Version == "" {
		return "dev"
	}
	return r.Version
}

func (r *Responder) start(in Incoming) Reply {
	var b strings.Builder
	fmt.Fprintf(&b, "<b>⟁ Telegraph</b> %s\n", esc(r.ver()))
	b.WriteString("Decentralised handles like <code>alice@nym</code>, signed and gossiped over Telegram.\n\n")
	if r.Priv != nil {
		b.WriteString("/register <i>name</i> — claim name@nym\n/card — your bio line, tap to copy\n")
	} else if ref := r.referral(); ref != "" {
		b.WriteString("To claim a handle, open " + esc(ref) + " and send /register <i>name</i>.\n")
	}
	b.WriteString("/resolve <i>handle</i> — look up a handle\n/stats — network counts\n/help — all commands")
	return r.reply(in, b.String(), r.linkButtons())
}

func (r *Responder) help(in Incoming) Reply {
	var b strings.Builder
	b.WriteString("<b>Commands</b>\n")
	for _, c := range r.Menu() {
		b.WriteString("/" + c.Command)
		for _, x := range Commands {
			if x.Name == c.Command && x.Args != "" && (r.Priv != nil || !custodialOnly(x.Name)) {
				b.WriteString(" <i>" + esc(x.Args) + "</i>")
			}
		}
		b.WriteString(" — " + esc(c.Description) + "\n")
	}
	b.WriteString("\nOr send a handle on its own, like <code>alice@nym</code>.")
	return r.reply(in, b.String(), nil)
}

func (r *Responder) about(in Incoming) Reply {
	return r.reply(in, "<b>About Telegraph</b>\n"+
		"• Every node keeps a signed, hash-chained log of handle entries.\n"+
		"• Nodes gossip entries on a public Telegram channel and merge them deterministically.\n"+
		"• First come, first served: only a handle's owner can update, revoke or swap it.\n"+
		"• A committee of three nodes signs checkpoints; two signatures make one valid.\n"+
		"• New nodes trust only history that leads back to the published pin (/pin).\n\n"+
		"No blockchain, no central server. Run your own node: <a href=\""+DefaultNodeURL+"\">README</a>",
		r.linkButtons())
}

func (r *Responder) version(in Incoming) Reply {
	return r.reply(in, "<b>⟁ Telegraph</b> "+esc(r.ver()),
		[][]Button{{{Text: "Releases", URL: DefaultReleaseURL}}})
}

// Replies for the common failure kinds, so every command words them alike.

func (r *Responder) usage(in Incoming, form string) Reply {
	return r.reply(in, "⚠️ Usage: <code>"+esc(form)+"</code>", nil)
}

func (r *Responder) badInput(in Incoming, why string) Reply {
	return r.reply(in, "⚠️ "+esc(why), nil)
}

func (r *Responder) unknownHandle(in Incoming, handle string) Reply {
	msg := "⚠️ <b>" + esc(handle) + "</b> is not registered."
	if strings.HasSuffix(handle, "@"+Namespace) {
		name := esc(strings.TrimSuffix(handle, "@"+Namespace))
		if r.Priv != nil {
			msg += " It is free: /register " + name
		} else if ref := r.referral(); ref != "" {
			msg += " It is free: send /register " + name + " to " + esc(ref) + "."
		}
	}
	return r.reply(in, msg, nil)
}

func (r *Responder) rateLimited(in Incoming, wait time.Duration) Reply {
	return r.reply(in, fmt.Sprintf("⏳ Too many messages. Try again in %d s.", int(wait.Seconds())), nil)
}

// internalError logs err for the operator and tells the user only that
// something failed: no error text, paths or keys reach the chat.
func (r *Responder) internalError(in Incoming, op string, err error) Reply {
	l := r.Log
	if l == nil {
		l = slog.Default()
	}
	l.Error("bot command failed", "op", op, "err", err)
	return r.reply(in, "⚠️ Something went wrong on this node. Please try again in a minute.", nil)
}

func (r *Responder) resolve(st *store.Store, in Incoming, handle string) Reply {
	switch {
	case utf8.RuneCountInString(handle) > maxHandleLen:
		return r.badInput(in, "Handles are at most 64 characters.")
	case !looksLikeHandle(handle):
		return r.reply(in, "⚠️ <code>"+esc(clip(handle, 64))+"</code> is not a handle. Handles look like <code>alice@nym</code>.", nil)
	}
	e, status, ok := lookupHandle(st, handle)
	if !ok {
		return r.unknownHandle(in, handle)
	}
	if e.Payload.Kind == tlog.KindRevoke {
		msg := "<b>" + esc(handle) + "</b> was revoked by its owner and is free."
		if strings.HasSuffix(handle, "@"+Namespace) {
			name := esc(strings.TrimSuffix(handle, "@"+Namespace))
			if r.Priv != nil {
				msg += " /register " + name + " to claim it."
			} else if ref := r.referral(); ref != "" {
				msg += " Send /register " + name + " to " + esc(ref) + " to claim it."
			}
		}
		return r.reply(in, msg, nil)
	}
	did := "<i>none yet (new owner has not set one)</i>"
	if e.Payload.Kind == tlog.KindRegister {
		did = "<code>" + esc(clip(e.Payload.DID, 200)) + "</code>"
	}
	rep := r.audit(st)
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
		b.WriteString("No entries yet.")
		return r.reply(in, b.String(), nil)
	}
	ns := Stats(st, r.now())
	fmt.Fprintf(&b, "Entries %d · handles %d · issuers %d\n",
		len(st.Entries), ns.Active+ns.Revoked, len(checkpoint.Issuers(st.Entries)))
	rep := r.audit(st)
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
