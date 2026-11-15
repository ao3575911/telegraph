package transport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/ao3575911/telegraph/internal/handshake"
	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

// card answers /card [name]: the user's bio line "⟁ handle@nym · hex8"
// for each handle this node holds for them, each in a code entity so it
// copies with one tap. The node signs a card attestation the first time,
// so `telegraph scan` reports the line VERIFIED.
func (r *Responder) card(st *store.Store, in Incoming, arg string) Reply {
	if r.Priv == nil {
		return r.reply(in, "This node does not hold handles for Telegram users. "+
			"With the CLI, run <code>telegraph card --handle you@nym</code>.", nil)
	}
	if in.User == 0 && !in.Private {
		return r.badInput(in, "I cannot tell who sent that. Ask me in a private chat.")
	}
	user := sender(in)
	handles := heldHandles(st, r.pub(), user)
	if f := strings.Fields(arg); len(f) > 0 {
		if len(f) > 1 {
			return r.usage(in, "/card  or  /card name")
		}
		h, err := NormaliseName(f[0])
		if err != nil {
			return r.badInput(in, sentence(err.Error()))
		}
		found := false
		for _, x := range handles {
			found = found || x == h
		}
		if !found {
			return r.reply(in, "⚠️ You do not hold <b>"+h+"</b> here. /card lists the handles you hold.", nil)
		}
		handles = []string{h}
	}
	if len(handles) == 0 {
		return r.reply(in, "You have no handle on this node yet. Claim one with /register <i>name</i>.\n"+
			"Registered with the CLI? Run <code>telegraph card --handle you@nym</code>.", nil)
	}
	var b strings.Builder
	if len(handles) == 1 {
		b.WriteString("Your bio line (tap to copy):\n")
	} else {
		b.WriteString("Your bio lines (tap one to copy):\n")
	}
	for _, h := range handles {
		e, _, ok := lookupHandle(st, h)
		if !ok || e.Payload.Kind != tlog.KindRegister {
			continue
		}
		line := handshake.CardLine(h, &e)
		if err := r.attestCard(st, h, line); err != nil {
			return r.internalError(in, "card", err)
		}
		b.WriteString("<code>" + esc(line) + "</code>\n")
	}
	b.WriteString("\nPaste it into your Telegram bio. Anyone can check it with /resolve or <code>telegraph scan</code>.")
	return r.reply(in, b.String(), nil)
}

// attestCard signs a card entry for line unless this node already has.
func (r *Responder) attestCard(st *store.Store, handle, line string) error {
	if cardAttested(st, handle, line) {
		return nil
	}
	e, err := handshake.BuildCard(line, st.NextTS(r.pub(), r.now().Unix()), r.Priv, st.Tip(r.pub()))
	if err != nil {
		return err
	}
	if !st.AppendEntry(e) {
		return errRejected
	}
	if err := st.Save(); err != nil {
		r.logErr("save after card", err)
	}
	return nil
}

// cardAttested is handshake.VerifyCard over the store's indexes.
func cardAttested(st *store.Store, handle, line string) bool {
	o := st.Ownership(handle)
	if o.Free {
		return false
	}
	sum := sha256.Sum256([]byte(line))
	want := hex.EncodeToString(sum[:])
	for _, e := range st.HandleEntries("card") {
		if e.Payload.Kind == tlog.KindCard && e.Payload.DID == line && e.Payload.Hash == want &&
			bytes.Equal(e.PubKey, o.Owner) && e.Verify() == nil {
			return true
		}
	}
	return false
}
