package transport

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/checkpoint"
)

// Two nodes register the same handle before they sync. Both must keep
// both entries, pick the same owner (the earlier register), checkpoint the
// same log and pass audit. Before the fix each node refused the other's
// register as a "backdated takeover" and they diverged forever.
func TestConcurrentRegisterConverges(t *testing.T) {
	for _, laterFirst := range []bool{false, true} {
		dir := t.TempDir()
		bus := filepath.Join(dir, "bus.jsonl")
		a, b := newNode(t, dir, bus, "a"), newNode(t, dir, bus, "b")
		past := time.Now().Unix() - 120
		a.register(t, "x@nym", past)
		b.register(t, "x@nym", past+5)
		a.register(t, "a@nym", past+6)
		b.register(t, "b@nym", past+7)
		order := []*node{a, b}
		if laterFirst { // the later register reaches the bus first
			order = []*node{b, a}
		}
		for round := 0; round < 3; round++ {
			for _, n := range order {
				n.sync(t)
			}
		}
		for i, n := range []*node{a, b} {
			if len(n.st.Entries) != 4 {
				t.Fatalf("laterFirst=%v node %d: %d entries, want 4", laterFirst, i, len(n.st.Entries))
			}
			o := n.st.Ownership("x@nym")
			if !bytes.Equal(o.Owner, a.sy.pub()) {
				t.Fatalf("laterFirst=%v node %d: x@nym not owned by the earliest register", laterFirst, i)
			}
			if e, _, ok := lookupHandle(n.st, "x@nym"); !ok || e.Payload.TS != past {
				t.Fatalf("laterFirst=%v node %d: resolve disagrees", laterFirst, i)
			}
		}
		issued := 0
		for _, n := range order {
			cp, err := n.sy.IssueCheckpoint()
			dieOnT(t, err)
			if cp != nil {
				issued++
			}
		}
		if issued != 1 {
			t.Fatalf("%d checkpoints issued", issued)
		}
		for round := 0; round < 3; round++ {
			for _, n := range order {
				n.sync(t)
			}
		}
		var root string
		for i, n := range []*node{a, b} {
			rep := checkpoint.Audit(n.st.Entries)
			if rep.NoCheckpoint || rep.Diverged || rep.PendingCP != 0 || rep.Uncovered != 0 {
				t.Fatalf("laterFirst=%v node %d: diverged=%v pending=%d uncovered=%d gaps=%v",
					laterFirst, i, rep.Diverged, rep.PendingCP, rep.Uncovered, rep.ChainGaps)
			}
			if root != "" && rep.CoveredRoot != root {
				t.Fatal("nodes checkpoint different logs")
			}
			root = rep.CoveredRoot
			if !bytes.Equal(n.st.Ownership("x@nym").Owner, a.sy.pub()) {
				t.Fatalf("node %d: owner changed after checkpoint", i)
			}
		}
	}
}

// --genesis cannot skip the pin: a node whose window carries a checkpoint
// it is not on the committee of merges nothing.
func TestGenesisSyncRefusesPastGenesis(t *testing.T) {
	dir := t.TempDir()
	bus := filepath.Join(dir, "bus.jsonl")
	a, b := newNode(t, dir, bus, "a"), newNode(t, dir, bus, "b")
	past := time.Now().Unix() - 120
	a.register(t, "a@nym", past)
	b.register(t, "b@nym", past+1)
	for round := 0; round < 2; round++ {
		a.sync(t)
		b.sync(t)
	}
	for _, n := range []*node{a, b} {
		_, err := n.sy.IssueCheckpoint()
		dieOnT(t, err)
	}
	for round := 0; round < 2; round++ {
		a.sync(t)
		b.sync(t)
	}
	c := newNode(t, dir, bus, "c")
	c.register(t, "c@nym", past+2)
	if _, err := c.sy.GenesisSync(0); err != ErrGenesisHasCP {
		t.Fatalf("GenesisSync past genesis: %v", err)
	}
	if len(c.st.Entries) != 1 {
		t.Fatalf("refused window merged %d entries", len(c.st.Entries)-1)
	}
	// before any checkpoint, a genesis window merges normally
	d, e := newNode(t, dir, filepath.Join(dir, "bus2"), "d"), newNode(t, dir, filepath.Join(dir, "bus2"), "e")
	d.register(t, "d@nym", past)
	_, err := d.sy.PublishPending()
	dieOnT(t, err)
	if n, err := e.sy.GenesisSync(0); err != nil || n != 1 {
		t.Fatalf("genesis window: %d %v", n, err)
	}
}
