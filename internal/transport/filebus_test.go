package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/checkpoint"
	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

type node struct {
	st *store.Store
	sy *Syncer
}

func newNode(t *testing.T, dir, bus, name string) *node {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	dieOnT(t, err)
	fb, err := OpenFileBus(bus, filepath.Join(dir, name+".offset"))
	dieOnT(t, err)
	st, err := store.Open(filepath.Join(dir, name+".json"))
	dieOnT(t, err)
	return &node{st: st, sy: NewSyncer([]Conn{fb}, []string{"local"}, st, priv)}
}

func (n *node) register(t *testing.T, handle string, ts int64) {
	t.Helper()
	e, err := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: handle,
		DID: "did:" + handle, TS: ts, Nonce: randData(t, 16)}, n.sy.Priv, n.st.Tip(n.sy.pub()))
	dieOnT(t, err)
	if !n.st.AppendEntry(e) {
		t.Fatal("own entry rejected")
	}
}

func (n *node) sync(t *testing.T) int {
	t.Helper()
	_, err := n.sy.PublishPending()
	dieOnT(t, err)
	m, err := n.sy.PollWindow(0)
	dieOnT(t, err)
	return m
}

// Three nodes on one file bus converge, the committee leader checkpoints,
// a second member co-signs over the bus, and every node's audit passes.
func TestFileBusThreeNodeCheckpoint(t *testing.T) {
	dir := t.TempDir()
	bus := filepath.Join(dir, "bus.jsonl")
	nodes := []*node{newNode(t, dir, bus, "a"), newNode(t, dir, bus, "b"), newNode(t, dir, bus, "c")}
	past := time.Now().Unix() - 60
	for i, h := range []string{"alice@nym", "bob@nym", "carol@nym"} {
		nodes[i].register(t, h, past+int64(i))
	}
	for round := 0; round < 2; round++ {
		for _, n := range nodes {
			n.sync(t)
		}
	}
	for _, n := range nodes {
		if len(n.st.Entries) != 3 {
			t.Fatalf("node has %d entries, want 3", len(n.st.Entries))
		}
	}
	issued := 0
	for _, n := range nodes {
		cp, err := n.sy.IssueCheckpoint()
		dieOnT(t, err)
		if cp != nil {
			issued++
		}
	}
	if issued != 1 {
		t.Fatalf("%d checkpoints issued, want exactly 1 (the leader)", issued)
	}
	for round := 0; round < 3; round++ {
		for _, n := range nodes {
			n.sync(t)
		}
	}
	for i, n := range nodes {
		rep := checkpoint.Audit(n.st.Entries)
		if rep.NoCheckpoint || rep.Diverged || rep.PendingCP != 0 {
			t.Fatalf("node %d: cp=%v diverged=%v pending=%d gaps=%v", i,
				!rep.NoCheckpoint, rep.Diverged, rep.PendingCP, rep.ChainGaps)
		}
	}
}

func TestFileBusOffsetPersists(t *testing.T) {
	dir := t.TempDir()
	bus, off := filepath.Join(dir, "bus"), filepath.Join(dir, "off")
	w, err := OpenFileBus(bus, "")
	dieOnT(t, err)
	dieOnT(t, w.Send("c", "one"))
	r, err := OpenFileBus(bus, off)
	dieOnT(t, err)
	ups, err := r.Poll(0)
	dieOnT(t, err)
	if len(ups) != 1 || ups[0].ChannelPost.Text != "one" {
		t.Fatalf("got %+v", ups)
	}
	dieOnT(t, w.Send("c", "two"))
	r2, err := OpenFileBus(bus, off) // a later process resumes after "one"
	dieOnT(t, err)
	ups, err = r2.Poll(0)
	dieOnT(t, err)
	if len(ups) != 1 || ups[0].ChannelPost.Text != "two" {
		t.Fatalf("resume got %+v", ups)
	}
}
