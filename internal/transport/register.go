package transport

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ao3575911/telegraph/internal/handshake"
	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

// Bot registration is custodial. The node signs the register entry with
// its own key, so on the network the node's key owns the handle. The node
// records the Telegram user id in store.Custody and only that user can
// change the DID or revoke the handle through the bot. The bot follows
// the network's first-come rule: it never signs for a handle another key
// owns, a handle the operator holds without custody, or one locked in a
// swap.

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
	return r.reply(in, "🔒 /"+cmd+" works only in a private chat with me.", buttons)
}

// noRegister is the reply when this node has bot registration turned off.
func (r *Responder) noRegister(in Incoming) Reply {
	return r.reply(in, "This node does not hold handles for Telegram users. "+
		"Register with your own key using the CLI: <a href=\""+DefaultNodeURL+"\">install</a>.", nil)
}

const registerUsage = "/register name  or  /register name did:method:id"

func (r *Responder) register(st *store.Store, in Incoming, arg string) Reply {
	switch {
	case !in.Private:
		return r.dmOnly(in, "register")
	case r.Priv == nil:
		return r.noRegister(in)
	}
	user := sender(in)
	handle, did, defaulted, err := parseRegister(arg, user)
	if err != nil {
		if err.Error() == "usage" {
			return r.usage(in, registerUsage)
		}
		return r.badInput(in, sentence(err.Error()))
	}
	prev, refusal := r.checkOwner(st, handle, user)
	if refusal != "" {
		return r.reply(in, refusal, nil)
	}
	if prev != nil && prev.Payload.Kind == tlog.KindRegister && prev.Payload.DID == did {
		return r.reply(in, "<b>"+handle+"</b> is already yours with DID <code>"+esc(did)+"</code>. /card for your bio line.", nil)
	}
	if prev == nil && held(st, r.pub(), user, handle) >= MaxHandlesPerUser {
		return r.reply(in, fmt.Sprintf("⚠️ You already hold %d handles here, the limit per account. "+
			"/revoke one first.", MaxHandlesPerUser), nil)
	}
	// Bind custody before the entry exists, so a crash between the two
	// can never leave a node-signed handle without its Telegram owner.
	if st.Custody == nil {
		st.Custody = map[string]int64{}
	}
	hadCustody, had := st.Custody[handle]
	st.Custody[handle] = user
	e, err := r.sign(st, tlog.KindRegister, handle, did, prev)
	if err != nil {
		if had {
			st.Custody[handle] = hadCustody
		} else {
			delete(st.Custody, handle)
		}
		return r.internalError(in, "register", err)
	}
	if err := st.Save(); err != nil {
		r.logErr("save after register", err)
	}
	verb := "Registered"
	if prev != nil && prev.Payload.Kind == tlog.KindRegister {
		verb = "Updated"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "✅ %s <b>%s</b>\nDID <code>%s</code>\nEntry <code>%s</code>\n\n", verb, handle, esc(did), short8(e.HashHex()))
	fmt.Fprintf(&b, "Held for you by this node (key <code>%s</code>). Only your Telegram account can change or /revoke it.\n",
		short8(e.KeyHex()))
	if defaulted {
		b.WriteString("\nThe default DID shows your Telegram user id publicly. To use your own: <code>/register " +
			strings.TrimSuffix(handle, "@"+Namespace) + " did:method:id</code>\n")
	}
	if prev == nil {
		if n := ordinal(st, handle); n > 0 {
			if badge := Badge(n); badge != "" {
				fmt.Fprintf(&b, "\n%s · handle #%d on the network\n", badge, n)
			} else {
				fmt.Fprintf(&b, "\nHandle #%d on the network\n", n)
			}
		}
	}
	b.WriteString("\nNext: /card for your bio line.")
	return r.reply(in, b.String(), nil)
}

func (r *Responder) revoke(st *store.Store, in Incoming, arg string) Reply {
	switch {
	case !in.Private:
		return r.dmOnly(in, "revoke")
	case r.Priv == nil:
		return r.noRegister(in)
	}
	f := strings.Fields(arg)
	if len(f) != 1 {
		return r.usage(in, "/revoke name")
	}
	handle, err := NormaliseName(f[0])
	if err != nil {
		return r.badInput(in, sentence(err.Error()))
	}
	user := sender(in)
	prev, refusal := r.checkOwner(st, handle, user)
	switch {
	case refusal != "":
		return r.reply(in, refusal, nil)
	case prev == nil || prev.Payload.Kind == tlog.KindRevoke:
		return r.reply(in, "⚠️ You do not hold <b>"+handle+"</b> here.", nil)
	}
	if _, err := r.sign(st, tlog.KindRevoke, handle, "", prev); err != nil {
		return r.internalError(in, "revoke", err)
	}
	delete(st.Custody, handle)
	if err := st.Save(); err != nil {
		r.logErr("save after revoke", err)
	}
	return r.reply(in, "Revoked <b>"+handle+"</b>. It is free for anyone to register.", nil)
}

func (r *Responder) logErr(op string, err error) {
	l := r.Log
	if l == nil {
		l = slog.Default()
	}
	l.Error("bot", "op", op, "err", err)
}

// sentence capitalises s and ends it with a full stop unless it already
// ends in punctuation.
func sentence(s string) string {
	if s == "" {
		return s
	}
	s = strings.ToUpper(s[:1]) + s[1:]
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "…") && !strings.HasSuffix(s, "?") && !strings.HasSuffix(s, "!") {
		s += "."
	}
	return s
}

// checkOwner decides whether user may write handle through this node,
// following the network's first-come ownership (issue #15). It returns
// the handle's current entry when this node holds it for user (nil when
// the handle is free) or a refusal to send instead. Custody records that
// no longer match the network (revoked, or swapped away) are dropped.
func (r *Responder) checkOwner(st *store.Store, handle string, user int64) (*tlog.Entry, string) {
	o := st.Ownership(handle)
	cust, custodial := st.Custody[handle]
	switch {
	case o.Status == handshake.StateLocked:
		return nil, "⏳ <b>" + handle + "</b> is locked in a swap. Try again after it closes."
	case o.Free:
		if custodial {
			delete(st.Custody, handle)
		}
		return nil, ""
	case !bytes.Equal(o.Owner, r.pub()):
		if custodial {
			delete(st.Custody, handle) // swapped away from this node
		}
		return nil, "⚠️ <b>" + handle + "</b> is already registered to another key. /resolve " + handle
	case !custodial:
		return nil, "⚠️ <b>" + handle + "</b> is reserved by this node's operator."
	case cust != user:
		return nil, "⚠️ <b>" + handle + "</b> is taken by another Telegram user."
	}
	e, _, ok := lookupHandle(st, handle)
	if !ok {
		return nil, ""
	}
	return &e, ""
}

// held counts the handles user holds through this node, other than
// handle: custody records whose handle this node's key still owns.
func held(st *store.Store, node []byte, user int64, handle string) int {
	n := 0
	for h, u := range st.Custody {
		if u != user || h == handle {
			continue
		}
		if o := st.Ownership(h); !o.Free && bytes.Equal(o.Owner, node) {
			n++
		}
	}
	return n
}

// heldHandles lists, sorted, the handles user holds through this node.
func heldHandles(st *store.Store, node []byte, user int64) []string {
	var out []string
	for h, u := range st.Custody {
		if u != user {
			continue
		}
		if o := st.Ownership(h); !o.Free && bytes.Equal(o.Owner, node) && o.Status != handshake.StateLocked {
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}

// sign appends a node-signed register or revoke entry, timestamped after
// prev so the owner's update orders after its previous entry.
func (r *Responder) sign(st *store.Store, kind tlog.Kind, handle, did string, prev *tlog.Entry) (*tlog.Entry, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ts := st.NextTS(r.pub(), r.now().Unix())
	if prev != nil && ts <= prev.Payload.TS {
		ts = prev.Payload.TS + 1
	}
	e, err := tlog.New(tlog.Payload{Kind: kind, Handle: handle, DID: did, TS: ts, Nonce: nonce},
		r.Priv, st.Tip(r.pub()))
	if err != nil {
		return nil, err
	}
	if !st.AppendEntry(e) {
		return nil, errRejected
	}
	return e, nil
}

var errRejected = errors.New("entry rejected by local store")
