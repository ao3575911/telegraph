package transport

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ao3575911/telegraph/internal/handshake"
	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

// Bot registration is custodial. The node signs the register entry with
// its own key, so on the network the node's key owns the handle. The node
// records the Telegram user id in store.Custody and only that user can
// change the DID or revoke the handle through the bot.

// MaxHandlesPerUser caps how many handles one Telegram user can hold
// through one node.
const MaxHandlesPerUser = 3

// Namespace is the only namespace the bot registers in.
const Namespace = "nym"

var (
	nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,31}$`)
	didRE  = regexp.MustCompile(`^did:[a-z0-9]+:[A-Za-z0-9._:%-]+$`)

	errBadName = errors.New("names are 1–32 characters: a–z, 0–9, and _ . - (not first)")
	errBadDID  = errors.New("a DID looks like did:method:id, for example did:key:z6Mk…")
)

const maxDIDLen = 200

// NormaliseName turns "Adz" or "adz@nym" into "adz@nym". The result also
// passes tlog.ValidHandle, the rule the CLI uses.
func NormaliseName(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if name, ns, ok := strings.Cut(s, "@"); ok {
		if ns != Namespace {
			return "", fmt.Errorf("the bot registers only @%s handles", Namespace)
		}
		s = name
	}
	if !nameRE.MatchString(s) {
		return "", errBadName
	}
	h := s + "@" + Namespace
	return h, tlog.ValidHandle(h)
}

// ValidDID checks the did:method:id shape (W3C DID syntax, simplified).
func ValidDID(d string) error {
	if len(d) > maxDIDLen || !didRE.MatchString(d) {
		return errBadDID
	}
	return nil
}

// TelegramDID is the default DID for a user who does not supply one. It
// is a plain label binding the handle to a Telegram account, not a
// resolvable DID method, and it makes the user id public.
func TelegramDID(user int64) string { return "did:tg:" + strconv.FormatInt(user, 10) }

// parseRegister splits "/register" arguments into a handle and DID.
func parseRegister(arg string, user int64) (handle, did string, defaulted bool, err error) {
	f := strings.Fields(arg)
	if len(f) == 0 || len(f) > 2 {
		return "", "", false, errors.New("usage")
	}
	if handle, err = NormaliseName(f[0]); err != nil {
		return "", "", false, err
	}
	if len(f) == 2 {
		return handle, f[1], false, ValidDID(f[1])
	}
	return handle, TelegramDID(user), true, nil
}

// sender is the Telegram user behind a private message. In a private
// chat the chat id is the user id, so it stands in when From is missing.
func sender(in Incoming) int64 {
	if in.User != 0 {
		return in.User
	}
	return in.Chat
}

func (r *Responder) pub() []byte { return []byte(r.Priv.Public().(ed25519.PublicKey)) }

// dmOnly is the reply to /register or /revoke outside a private chat.
func (r *Responder) dmOnly(in Incoming, cmd string) Reply {
	var buttons [][]Button
	if r.BotUsername != "" {
		buttons = [][]Button{{{Text: "Open a private chat", URL: "https://t.me/" + r.BotUsername}}}
	}
	return r.reply(in, "/"+cmd+" works only in a private chat with me.", buttons)
}

func (r *Responder) register(st *store.Store, in Incoming, arg string) Reply {
	switch {
	case !in.Private:
		return r.dmOnly(in, "register")
	case r.Priv == nil:
		return r.reply(in, "This node does not register handles. Register with the CLI on your own node: "+
			"<a href=\""+DefaultNodeURL+"\">README</a>", nil)
	}
	user := sender(in)
	handle, did, defaulted, err := parseRegister(arg, user)
	if err != nil {
		if err.Error() == "usage" {
			return r.reply(in, "Usage: <code>/register name</code> or <code>/register name did:method:id</code>", nil)
		}
		return r.reply(in, "⚠️ "+esc(err.Error())+".", nil)
	}
	prev, refusal := r.checkOwner(st, handle, user)
	if refusal != "" {
		return r.reply(in, refusal, nil)
	}
	if prev != nil && prev.Payload.Kind == tlog.KindRegister && prev.Payload.DID == did {
		return r.reply(in, "<b>"+handle+"</b> is already yours with DID <code>"+esc(did)+"</code>.", nil)
	}
	if prev == nil && held(st, user, handle) >= MaxHandlesPerUser {
		return r.reply(in, fmt.Sprintf("You already hold %d handles here, the most one account can. "+
			"/revoke one first.", MaxHandlesPerUser), nil)
	}
	e, err := r.sign(st, tlog.KindRegister, handle, did, prev)
	if err != nil {
		return r.reply(in, "⚠️ Could not register right now. Try again later.", nil)
	}
	if st.Custody == nil {
		st.Custody = map[string]int64{}
	}
	st.Custody[handle] = user
	_ = st.Save()
	verb := "Registered"
	if prev != nil && prev.Payload.Kind == tlog.KindRegister {
		verb = "Updated"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "✅ %s <b>%s</b>\nDID <code>%s</code>\nEntry <code>%s</code>\n\n", verb, handle, esc(did), short8(e.HashHex()))
	fmt.Fprintf(&b, "<b>Custodial:</b> this node signed it with its key <code>%s</code>, so on the network the node owns it. "+
		"Only your Telegram account can change or /revoke it here. To hold the key yourself, run your own node.\n\n",
		short8(e.KeyHex()))
	if defaulted {
		b.WriteString("The default DID contains your Telegram user id, and it is public. " +
			"Send <code>/register " + strings.TrimSuffix(handle, "@"+Namespace) + " did:method:id</code> to use your own.\n\n")
	}
	if prev == nil {
		if n := ordinal(st, handle); n > 0 {
			if badge := Badge(n); badge != "" {
				fmt.Fprintf(&b, "%s: handle #%d on the network.\n\n", badge, n)
			} else {
				fmt.Fprintf(&b, "Handle #%d on the network. /stats for more.\n\n", n)
			}
		}
	}
	b.WriteString("Publishing to the channel now. Anyone can /resolve it.")
	return r.reply(in, b.String(), nil)
}

func (r *Responder) revoke(st *store.Store, in Incoming, arg string) Reply {
	switch {
	case !in.Private:
		return r.dmOnly(in, "revoke")
	case r.Priv == nil:
		return r.reply(in, "This node does not register handles, so it has nothing to revoke.", nil)
	}
	f := strings.Fields(arg)
	if len(f) != 1 {
		return r.reply(in, "Usage: <code>/revoke name</code>", nil)
	}
	handle, err := NormaliseName(f[0])
	if err != nil {
		return r.reply(in, "⚠️ "+esc(err.Error())+".", nil)
	}
	user := sender(in)
	prev, refusal := r.checkOwner(st, handle, user)
	switch {
	case refusal != "":
		return r.reply(in, refusal, nil)
	case prev == nil || prev.Payload.Kind == tlog.KindRevoke:
		return r.reply(in, "<b>"+handle+"</b> is not registered to you.", nil)
	}
	if _, err := r.sign(st, tlog.KindRevoke, handle, "", prev); err != nil {
		return r.reply(in, "⚠️ Could not revoke right now. Try again later.", nil)
	}
	delete(st.Custody, handle)
	_ = st.Save()
	return r.reply(in, "Revoked <b>"+handle+"</b>. It is free for anyone to register.", nil)
}

// checkOwner decides whether user may write handle through this node. It
// returns the handle's current entry when this node holds it for user
// (nil when the handle is free) or a refusal to send instead.
func (r *Responder) checkOwner(st *store.Store, handle string, user int64) (*tlog.Entry, string) {
	reg := lookup(st)
	if owner, ok := st.Custody[handle]; ok && owner != user {
		return nil, "<b>" + handle + "</b> is taken by another Telegram user."
	}
	e, status, ok := reg.Resolve(handle)
	if !ok {
		return nil, ""
	}
	if status == handshake.StateLocked {
		return nil, "<b>" + handle + "</b> is locked in a swap. Try again later."
	}
	mine := bytes.Equal(e.PubKey, r.pub())
	_, custodial := st.Custody[handle]
	switch {
	case mine && custodial:
		return &e, "" // custody is user's (checked above)
	case e.Payload.Kind == tlog.KindRevoke:
		return nil, "" // revoked: free again
	case mine:
		return nil, "<b>" + handle + "</b> belongs to this node's operator."
	}
	return nil, "<b>" + handle + "</b> is already registered by someone else."
}

// held counts the handles user holds through this node, other than handle.
func held(st *store.Store, user int64, handle string) int {
	n := 0
	for h, u := range st.Custody {
		if u == user && h != handle {
			n++
		}
	}
	return n
}

// sign appends a node-signed register or revoke entry. Its timestamp is
// after prev's so it wins last-writer-wins even within one second.
func (r *Responder) sign(st *store.Store, kind tlog.Kind, handle, did string, prev *tlog.Entry) (*tlog.Entry, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ts := r.now().Unix()
	if prev != nil && ts <= prev.Payload.TS {
		ts = prev.Payload.TS + 1
	}
	e, err := tlog.New(tlog.Payload{Kind: kind, Handle: handle, DID: did, TS: ts, Nonce: nonce},
		r.Priv, st.Tip(r.pub()))
	if err != nil {
		return nil, err
	}
	if !st.AppendEntry(e) {
		return nil, errors.New("entry rejected by local store")
	}
	return e, nil
}
