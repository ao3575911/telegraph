package handshake

import (
	"bytes"

	"github.com/ao3575911/telegraph/internal/tlog"
)

type Status int

const (
	StateRegistered Status = iota // plain LWW world
	StateLocked                   // active lock, frozen
	StateOwned                    // claim closed the swap — new owner
	StateHeld                     // reclaim closed the swap — owner unchanged
)

func (s Status) String() string {
	switch s {
	case StateLocked:
		return "LOCKED"
	case StateOwned:
		return "SWAPPED"
	case StateHeld:
		return "REVERTED"
	}
	return "REGISTERED"
}

// Owner deterministically folds all rule-valid activity for a handle.
// Every node with the same entry set derives the same owner.
//
// Ownership is first-come (v0.3.0, fixes #15): the earliest valid
// register in causal order takes a free handle, and from then on only the
// owner's own register, revoke or lock entries count. A register from any
// other key is void, so a later (or louder) key cannot take a handle over.
// A revoke by the owner frees the handle for the next register.
// Concurrent registers from different keys are settled the same way on
// every node: earliest first, except that a register a trusted checkpoint
// has finalised beats any uncovered register not dated after it (see
// finality.go).
//
// A lock only counts when its signer owned the handle immediately before
// the lock (computed recursively over the strictly earlier entries), so a
// stranger cannot freeze or hijack a handle by publishing a lock for it.
// While a valid lock is open, registers are void (they would let the old
// owner re-key and double-take). The swap closes on the earliest valid
// claim, else on the earliest valid reclaim; after it closes the fold
// continues with the claimant (or the original owner) as owner.
//
// When the newest plain entry is the owner's revoke, Owner still returns
// the revoker's key so callers can show "revoked"; Free reports whether
// the handle can be claimed.
func Owner(handle string, entries []tlog.Entry) (pub []byte, st Status, since int64) {
	r := ownerOf(handle, entries, entries)
	return r.pub, r.st, r.since
}

// Free reports whether handle has no owner: never registered, or revoked
// by its last owner. A register from any key would take it.
func Free(handle string, entries []tlog.Entry) bool {
	r := ownerOf(handle, entries, entries)
	return r.pub == nil || r.revoked
}

// Tenure returns the current owner and the timestamp of the entry that
// gave it the handle (its first register, or the claim that closed a
// swap). pub is nil and acquired 0 when the handle is free.
func Tenure(handle string, entries []tlog.Entry) (pub []byte, acquired int64) {
	o := Resolve(handle, entries, entries)
	return o.Owner, o.Acquired
}

// Ownership is the full result of the ownership fold for one handle.
type Ownership struct {
	Pub      []byte // key of the newest counted entry (the revoker when Free after a revoke)
	Owner    []byte // current owner; nil when Free
	Status   Status
	Since    int64 // timestamp of the newest counted entry
	Acquired int64 // when Owner took the handle; 0 when Free
	Free     bool
}

// Resolve folds handle over rel, a subset of all holding at least every
// entry for handle (for example a per-handle index). all is used only to
// order same-second entries along their signers' chains, so the result is
// identical to Owner(handle, all) at a fraction of the cost.
func Resolve(handle string, rel, all []tlog.Entry) Ownership {
	r := ownerOf(handle, rel, all)
	o := Ownership{Pub: r.pub, Status: r.st, Since: r.since, Free: r.pub == nil || r.revoked}
	if !o.Free {
		o.Owner, o.Acquired = r.pub, r.acquired
	}
	return o
}

func ownerOf(handle string, entries, all []tlog.Entry) result {
	var rel []*tlog.Entry
	for i := range entries {
		e := &entries[i]
		if e.Payload.Handle == handle && e.Verify() == nil {
			rel = append(rel, e)
		}
	}
	rel = dropUnfinalTakeovers(rel, all)
	tlog.SortCausal(rel, all)
	return (&folder{rel: rel, memo: map[int]result{}}).fold(len(rel))
}

type result struct {
	pub      []byte
	st       Status
	since    int64
	acquired int64
	revoked  bool
}

// folder memoises fold over prefixes of rel, keeping Owner polynomial
// even when many locks are present.
type folder struct {
	rel  []*tlog.Entry
	memo map[int]result
}

func (f *folder) fold(n int) result {
	if r, ok := f.memo[n]; ok {
		return r
	}
	r := f.compute(n)
	f.memo[n] = r
	return r
}

// compute resolves ownership over rel[:n]; rel is in tlog.SortCausal order.
func (f *folder) compute(n int) result {
	rel := f.rel[:n]
	// newest lock whose signer owned (and had not revoked) the handle just before it
	for i := len(rel) - 1; i >= 0; i-- {
		lk := rel[i]
		if lk.Payload.Kind != tlog.KindLock {
			continue
		}
		prior := f.fold(i)
		if prior.pub == nil || prior.revoked || !bytes.Equal(prior.pub, lk.PubKey) {
			continue
		}
		for j, e := range rel {
			if e.Payload.Kind == tlog.KindClaim && ValidClaim(lk, e) == nil {
				r := result{pub: e.PubKey, st: StateOwned, since: e.Payload.TS, acquired: e.Payload.TS}
				return plain(r, rel[max(i, j)+1:])
			}
		}
		for j, e := range rel {
			if e.Payload.Kind == tlog.KindReclaim && ValidReclaim(lk, e) == nil {
				r := result{pub: e.PubKey, st: StateHeld, since: e.Payload.TS, acquired: prior.acquired}
				return plain(r, rel[max(i, j)+1:])
			}
		}
		// registers in-window are void
		return result{pub: lk.PubKey, st: StateLocked, since: lk.Payload.TS, acquired: prior.acquired}
	}
	return plain(result{}, rel)
}

// plain folds register and revoke entries over rest, starting from r.
// The first register takes a free handle; after that only the owner's
// entries count.
func plain(r result, rest []*tlog.Entry) result {
	for _, e := range rest {
		k := e.Payload.Kind
		if k != tlog.KindRegister && k != tlog.KindRevoke {
			continue
		}
		free := r.pub == nil || r.revoked
		owner := !free && bytes.Equal(r.pub, e.PubKey)
		switch {
		case k == tlog.KindRegister && free:
			r = result{pub: e.PubKey, st: StateRegistered, since: e.Payload.TS, acquired: e.Payload.TS}
		case k == tlog.KindRegister && owner:
			r.since = e.Payload.TS
		case k == tlog.KindRevoke && owner:
			r = result{pub: e.PubKey, st: StateRegistered, since: e.Payload.TS, revoked: true}
		}
	}
	return r
}
