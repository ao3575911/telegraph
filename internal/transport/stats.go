package transport

import (
	"fmt"
	"strings"
	"time"

	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

// NetStats are the network counts behind /stats, computed in one pass
// over the local log. Entries in a store were verified on admission, so
// nothing is re-verified here.
type NetStats struct {
	Active, Revoked int // handles owned now · handles freed by their owner's revoke (ownership fold)
	Ever            int // distinct handles ever registered
	Issuers         int // distinct signing keys of register entries
	Online          int // peers with a heartbeat in the last 24 h
	Swaps           int // completed swap claims
	Checkpoints     int
	Custodial       int    // handles this bot holds for Telegram users
	Newest          string // most recently first-registered handle
}

// Stats counts st at now.
func Stats(st *store.Store, now time.Time) NetStats {
	var s NetStats
	handles := map[string]bool{}
	first := map[string]int64{}
	issuers := map[string]bool{}
	for i := range st.Entries {
		p := &st.Entries[i].Payload
		switch p.Kind {
		case tlog.KindRegister, tlog.KindRevoke:
			handles[p.Handle] = true
			if p.Kind == tlog.KindRegister {
				issuers[string(st.Entries[i].PubKey)] = true
				if t, ok := first[p.Handle]; !ok || p.TS < t {
					first[p.Handle] = p.TS
				}
			}
		case tlog.KindClaim:
			s.Swaps++
		case tlog.KindCheckpoint:
			s.Checkpoints++
		}
	}
	// The same winner logic as /resolve: first-come ownership, swaps and
	// finality (handshake.Resolve). The newest register or revoke from any
	// key used to decide, so a void register or a stranger's revoke
	// skewed the counts.
	for h := range handles {
		switch o := st.Ownership(h); {
		case !o.Free:
			s.Active++
		case o.Pub != nil:
			s.Revoked++
		}
	}
	var newestTS int64
	for h, t := range first {
		if t > newestTS || (t == newestTS && h > s.Newest) {
			newestTS, s.Newest = t, h
		}
	}
	s.Ever, s.Issuers, s.Custodial = len(first), len(issuers), len(st.Custody)
	for _, p := range st.Peers {
		if now.Unix()-p.LastSeen < 24*3600 {
			s.Online++
		}
	}
	return s
}

// Badge is the early-adopter title for the nth handle ever registered on
// the network, or "" past the first thousand.
func Badge(n int) string {
	switch {
	case n <= 0:
		return ""
	case n <= 100:
		return "🏅 Founding 100"
	case n <= 1000:
		return "🎖 Early adopter"
	}
	return ""
}

func (r *Responder) stats(st *store.Store, in Incoming) Reply {
	s := Stats(st, r.now())
	var b strings.Builder
	b.WriteString("<b>⟁ Telegraph network</b>\n")
	fmt.Fprintf(&b, "Handles %d active · %d revoked · %d ever\n", s.Active, s.Revoked, s.Ever)
	fmt.Fprintf(&b, "Registrars %d · nodes online %d\n", s.Issuers, s.Online)
	fmt.Fprintf(&b, "Swaps %d · checkpoints %d\n", s.Swaps, s.Checkpoints)
	if r.Priv != nil {
		fmt.Fprintf(&b, "Held by this bot for Telegram users %d", s.Custodial)
		if in.Private {
			if n := held(st, r.pub(), senderTag(st, in), ""); n > 0 {
				fmt.Fprintf(&b, " (yours %d of %d)", n, MaxHandlesPerUser)
			}
		}
		b.WriteString("\n")
	}
	if s.Newest != "" {
		fmt.Fprintf(&b, "Newest handle <b>%s</b>\n", esc(s.Newest))
	}
	switch {
	case s.Ever < 100:
		fmt.Fprintf(&b, "\n🏅 %d Founding 100 spots left. /register to claim one.", 100-s.Ever)
	case s.Ever < 1000:
		fmt.Fprintf(&b, "\n🎖 %d Early adopter spots left. /register to claim one.", 1000-s.Ever)
	}
	return r.reply(in, strings.TrimRight(b.String(), "\n"), nil)
}

// ordinal is handle's position among every handle ever registered,
// ordered by first registration (ties by name), or 0 if never.
func ordinal(st *store.Store, handle string) int {
	first := map[string]int64{}
	for i := range st.Entries {
		p := &st.Entries[i].Payload
		if p.Kind != tlog.KindRegister {
			continue
		}
		if t, ok := first[p.Handle]; !ok || p.TS < t {
			first[p.Handle] = p.TS
		}
	}
	mine, ok := first[handle]
	if !ok {
		return 0
	}
	n := 1
	for h, t := range first {
		if t < mine || (t == mine && h < handle) {
			n++
		}
	}
	return n
}
