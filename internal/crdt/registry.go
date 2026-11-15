package crdt

import (
	"bytes"
	"sort"

	"github.com/ao3575911/telegraph/internal/handshake"
	"github.com/ao3575911/telegraph/internal/tlog"
)

// Registry holds the full gossiped entry set (All) plus an LWW view of
// plain register/revoke entries (Head). Resolution of handles runs
// through handshake.Owner, which folds swap state deterministically.
type Registry struct {
	Head map[string]tlog.Entry
	All  []tlog.Entry
}

func New() *Registry {
	return &Registry{Head: map[string]tlog.Entry{}}
}

// FromEntries validates the batch whole (hostile gossip is rejected
// wholesale) then applies.
func FromEntries(es []tlog.Entry) (*Registry, error) {
	r := New()
	for i := range es {
		if err := es[i].Verify(); err != nil && !tlog.IsPendingQuorum(err) {
			return nil, err
		}
		r.Apply(es[i])
	}
	return r, nil
}

func (r *Registry) Apply(e tlog.Entry) {
	r.All = append(r.All, e)
	if e.Payload.Kind != tlog.KindRegister && e.Payload.Kind != tlog.KindRevoke {
		return // swap/cp/hb entries resolve via the Owner fold, not LWW
	}
	cur, ok := r.Head[e.Payload.Handle]
	if !ok || wins(e, cur) {
		r.Head[e.Payload.Handle] = e
	}
}

// Merge is commutative, associative and idempotent across All: the union
// is de-duplicated by entry hash and applied in canonical (hash) order, so
// every merge order yields the identical All slice and Head map.
func Merge(rs ...*Registry) *Registry {
	byHash := map[string]tlog.Entry{}
	for _, r := range rs {
		for i := range r.All {
			byHash[r.All[i].HashHex()] = r.All[i]
		}
	}
	keys := make([]string, 0, len(byHash))
	for h := range byHash {
		keys = append(keys, h)
	}
	sort.Strings(keys)
	out := New()
	for _, h := range keys {
		out.Apply(byHash[h])
	}
	return out
}

// Resolve returns the winning entry for a handle plus its swap status.
func (r *Registry) Resolve(handle string) (tlog.Entry, handshake.Status, bool) {
	pub, st, _ := handshake.Owner(handle, r.All)
	if pub == nil {
		return tlog.Entry{}, st, false
	}
	// newest register/revoke by the current owner, for display
	var best tlog.Entry
	found := false
	for i := range r.All {
		e := r.All[i]
		if e.Payload.Handle != handle || !bytes.Equal(e.PubKey, pub) {
			continue
		}
		if e.Payload.Kind == tlog.KindRegister || e.Payload.Kind == tlog.KindRevoke {
			if !found || wins(e, best) {
				best, found = e, true
			}
		}
	}
	if !found {
		// owner via claim: no register entry yet, so report the newest
		// entry the owner signed for this handle (the claim itself)
		for i := range r.All {
			e := r.All[i]
			if e.Payload.Handle == handle && bytes.Equal(e.PubKey, pub) &&
				(!found || wins(e, best)) {
				best, found = e, true
			}
		}
	}
	return best, st, true
}

func wins(a, b tlog.Entry) bool {
	if a.Payload.TS != b.Payload.TS {
		return a.Payload.TS > b.Payload.TS
	}
	if c := bytes.Compare(a.PubKey, b.PubKey); c != 0 {
		return c > 0
	}
	return bytes.Compare(a.Hash, b.Hash) > 0
}
