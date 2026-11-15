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

// Owner deterministically folds all rule-valid swap activity for a
// handle. Every node with the same entry set derives the same owner.
//
// A lock only counts when its signer owned the handle immediately before
// the lock (computed recursively over the strictly earlier entries), so a
// stranger cannot freeze or hijack a handle by publishing a lock for it.
// While a valid lock is open, registers are void (they would let the old
// owner re-key and double-take). The swap closes on the earliest valid
// claim, else on the earliest valid reclaim.
func Owner(handle string, entries []tlog.Entry) (pub []byte, st Status, since int64) {
	var rel []*tlog.Entry
	for i := range entries {
		e := &entries[i]
		if e.Payload.Handle == handle && e.Verify() == nil {
			rel = append(rel, e)
		}
	}
	tlog.SortCausal(rel, entries)
	return (&folder{rel: rel, memo: map[int]result{}}).fold(len(rel))
}

type result struct {
	pub   []byte
	st    Status
	since int64
}

// folder memoises fold over prefixes of rel, keeping Owner polynomial
// even when many locks are present.
type folder struct {
	rel  []*tlog.Entry
	memo map[int]result
}

func (f *folder) fold(n int) ([]byte, Status, int64) {
	if r, ok := f.memo[n]; ok {
		return r.pub, r.st, r.since
	}
	p, st, since := f.compute(n)
	f.memo[n] = result{p, st, since}
	return p, st, since
}

// compute resolves ownership over rel[:n]; rel is in tlog.SortCausal order.
func (f *folder) compute(n int) ([]byte, Status, int64) {
	rel := f.rel[:n]
	// newest lock whose signer owned the handle just before it
	for i := len(rel) - 1; i >= 0; i-- {
		lk := rel[i]
		if lk.Payload.Kind != tlog.KindLock {
			continue
		}
		prior, _, _ := f.fold(i)
		if prior == nil || !bytes.Equal(prior, lk.PubKey) {
			continue
		}
		for _, e := range rel {
			if e.Payload.Kind == tlog.KindClaim && ValidClaim(lk, e) == nil {
				return e.PubKey, StateOwned, e.Payload.TS
			}
		}
		for _, e := range rel {
			if e.Payload.Kind == tlog.KindReclaim && ValidReclaim(lk, e) == nil {
				return e.PubKey, StateHeld, e.Payload.TS
			}
		}
		return lk.PubKey, StateLocked, lk.Payload.TS // registers in-window are void
	}
	var best *tlog.Entry
	for _, e := range rel {
		if e.Payload.Kind == tlog.KindRegister || e.Payload.Kind == tlog.KindRevoke {
			best = e // rel is sorted, so the last plain entry wins (LWW)
		}
	}
	if best == nil {
		return nil, StateRegistered, 0
	}
	return best.PubKey, StateRegistered, best.Payload.TS
}
