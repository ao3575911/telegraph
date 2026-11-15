package tlog

import (
	"bytes"
	"container/heap"
	"sort"
)

// SortCausal sorts es into the canonical fold order: by timestamp, then,
// among entries sharing a second, signer-chain order (an entry always
// follows its own PrevHash ancestors), then hash. The order depends only
// on the entry set, never on input order, so every node folds alike.
//
// Timestamps have one-second resolution, so one signer can emit several
// entries in a second (register, then offer, then lock). Ordering those by
// hash alone is a coin flip and could put a lock before the register that
// makes it valid. all is the log the chains are walked in; it may be the
// same set es points into.
func SortCausal(es []*Entry, all []Entry) {
	sort.Slice(es, func(i, j int) bool { return tsHashLess(es[i], es[j]) })
	var c *chains
	for lo := 0; lo < len(es); {
		hi := lo + 1
		for hi < len(es) && es[hi].Payload.TS == es[lo].Payload.TS {
			hi++
		}
		if hi-lo > 1 && sharedSigner(es[lo:hi]) {
			if c == nil {
				c = newChains(es, all)
			}
			c.order(es[lo:hi])
		}
		lo = hi
	}
}

func tsHashLess(a, b *Entry) bool {
	if a.Payload.TS != b.Payload.TS {
		return a.Payload.TS < b.Payload.TS
	}
	return bytes.Compare(a.Hash, b.Hash) < 0
}

func sharedSigner(run []*Entry) bool {
	seen := make(map[string]bool, len(run))
	for _, e := range run {
		if seen[string(e.PubKey)] {
			return true
		}
		seen[string(e.PubKey)] = true
	}
	return false
}

// chains measures how far each entry sits along its signer's PrevHash
// chain. A descendant is always strictly deeper than its ancestors.
type chains struct {
	byHash map[string]*Entry
	depth  map[string]int
}

func newChains(es []*Entry, all []Entry) *chains {
	signers := map[string]bool{}
	for _, e := range es {
		signers[string(e.PubKey)] = true
	}
	c := &chains{byHash: map[string]*Entry{}, depth: map[string]int{}}
	for i := range all {
		if signers[string(all[i].PubKey)] {
			c.byHash[string(all[i].Hash)] = &all[i]
		}
	}
	for _, e := range es {
		c.byHash[string(e.Hash)] = e
	}
	return c
}

// depthOf walks back iteratively (chains can be millions long), stopping
// at genesis, an unknown parent, another signer's entry or a cycle.
func (c *chains) depthOf(e *Entry) int {
	var path []*Entry
	onPath := map[string]bool{}
	base := 0
	for cur := e; ; {
		k := string(cur.Hash)
		if d, ok := c.depth[k]; ok {
			base = d
			break
		}
		if onPath[k] {
			break
		}
		onPath[k] = true
		path = append(path, cur)
		p, ok := c.byHash[string(cur.PrevHash)]
		if !ok || !bytes.Equal(p.PubKey, cur.PubKey) {
			break
		}
		cur = p
	}
	for i := len(path) - 1; i >= 0; i-- {
		base++
		c.depth[string(path[i].Hash)] = base
	}
	return c.depth[string(e.Hash)]
}

// order rewrites a same-second run, which arrives hash-sorted: repeatedly
// take the smallest-hash entry that is the shallowest remaining one of its
// signer. Signers' own entries keep chain order; ties stay hash-ordered.
func (c *chains) order(run []*Entry) {
	idx := map[string]int{}
	var queues [][]*Entry
	for _, e := range run {
		k := string(e.PubKey)
		i, ok := idx[k]
		if !ok {
			i = len(queues)
			idx[k] = i
			queues = append(queues, nil)
		}
		queues[i] = append(queues[i], e)
	}
	h := &heads{}
	for _, q := range queues {
		sort.SliceStable(q, func(i, j int) bool { return c.depthOf(q[i]) < c.depthOf(q[j]) })
		*h = append(*h, q)
	}
	heap.Init(h)
	for i := range run {
		q := (*h)[0]
		run[i] = q[0]
		if len(q) == 1 {
			heap.Pop(h)
		} else {
			(*h)[0] = q[1:]
			heap.Fix(h, 0)
		}
	}
}

// heads is a min-heap of per-signer queues keyed by each queue's head hash.
type heads [][]*Entry

func (h heads) Len() int           { return len(h) }
func (h heads) Less(i, j int) bool { return bytes.Compare(h[i][0].Hash, h[j][0].Hash) < 0 }
func (h heads) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *heads) Push(x any)        { *h = append(*h, x.([]*Entry)) }
func (h *heads) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}
