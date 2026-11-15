package handshake

import (
	"bytes"
	"encoding/hex"

	"github.com/ao3575911/telegraph/internal/tlog"
)

// Concurrent registers (issue: duplicate handles). Two nodes can sign a
// register for the same free handle before either has seen the other's.
// Nodes used to refuse a register from another key dated at or before
// the owner's tenure (a "backdated takeover"), but which register a node
// held first depended on gossip order, so the two nodes refused each
// other's entry, kept different logs forever and failed verify and audit.
//
// Admission no longer depends on arrival order: every verifiable entry is
// kept, and the fold decides deterministically from the entry set alone.
// Before any checkpoint, the earliest register wins (timestamp, then
// signer-chain order, then entry hash; see tlog.SortCausal). Once a
// trusted checkpoint covers a register, that register is final: a
// register from another key that the checkpoint does not cover, and that
// is not dated after the final one, is void. It either arrived after the
// network had settled the handle or was backdated to steal it, and
// counting it would rewrite settled history.

// TrustedCheckpoint reports whether cp can finalise registers: a
// checkpoint naming its chain heads (v0.2.0+), with a committee of at
// least two keys and a quorum of their signatures (tlog.VerifyAt, which
// also applies the committee allowlist when one is configured).
func TrustedCheckpoint(cp *tlog.Entry) bool {
	return cp.Payload.Kind == tlog.KindCheckpoint && len(cp.Payload.Heads) > 0 &&
		len(cp.Payload.Comm) >= tlog.RequiredCPSigs && cp.Verify() == nil
}

// dropUnfinalTakeovers removes from rel (entries for one handle) every
// register voided by a trusted checkpoint in all, as described above. It
// returns rel unchanged unless registers from two or more keys exist.
func dropUnfinalTakeovers(rel []*tlog.Entry, all []tlog.Entry) []*tlog.Entry {
	var regs []*tlog.Entry
	signers := map[string]bool{}
	for _, e := range rel {
		if e.Payload.Kind == tlog.KindRegister {
			regs = append(regs, e)
			signers[string(e.PubKey)] = true
		}
	}
	if len(signers) < 2 {
		return rel
	}
	byHash := map[string]*tlog.Entry{}
	var cps []*tlog.Entry
	for i := range all {
		e := &all[i]
		if signers[string(e.PubKey)] {
			byHash[string(e.Hash)] = e
		}
		if e.Payload.Kind == tlog.KindCheckpoint && TrustedCheckpoint(e) {
			cps = append(cps, e)
		}
	}
	for _, e := range rel {
		byHash[string(e.Hash)] = e
	}
	void := map[string]bool{}
	for _, cp := range cps {
		cov := covers(cp, regs, byHash)
		for _, final := range regs {
			if !cov[string(final.Hash)] {
				continue
			}
			for _, r := range regs {
				if !cov[string(r.Hash)] && !bytes.Equal(r.PubKey, final.PubKey) &&
					r.Payload.TS <= final.Payload.TS {
					void[string(r.Hash)] = true
				}
			}
		}
	}
	if len(void) == 0 {
		return rel
	}
	out := make([]*tlog.Entry, 0, len(rel)-len(void))
	for _, e := range rel {
		if !void[string(e.Hash)] {
			out = append(out, e)
		}
	}
	return out
}

// covers returns which of regs lie on the chains cp names (each head back
// to genesis), walking only the chains of the registers' signers.
func covers(cp *tlog.Entry, regs []*tlog.Entry, byHash map[string]*tlog.Entry) map[string]bool {
	want := map[string]bool{}
	for _, r := range regs {
		want[string(r.Hash)] = true
	}
	out := map[string]bool{}
	seen := map[string]bool{}
	for _, hx := range cp.Payload.Heads {
		h, err := hex.DecodeString(hx)
		if err != nil {
			continue
		}
		for cur, ok := byHash[string(h)]; ok && !seen[string(cur.Hash)]; cur, ok = byHash[string(cur.PrevHash)] {
			seen[string(cur.Hash)] = true
			if want[string(cur.Hash)] {
				out[string(cur.Hash)] = true
			}
		}
	}
	return out
}
