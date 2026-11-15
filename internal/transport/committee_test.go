package transport

import (
	"bytes"
	"crypto/ed25519"
	"path/filepath"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/checkpoint"
	"github.com/ao3575911/telegraph/internal/handshake"
	"github.com/ao3575911/telegraph/internal/tlog"
)

// With TELEGRAPH_COMMITTEE set, a key that merely registers a handle
// never joins the committee, and a checkpoint it forges with an
// accomplice is kept but attests nothing: not the latest checkpoint, not
// a finality witness for a takeover.
func TestCommitteeAllowlistExcludesOutsiders(t *testing.T) {
	dir := t.TempDir()
	bus := filepath.Join(dir, "bus.jsonl")
	a, b, x := newNode(t, dir, bus, "a"), newNode(t, dir, bus, "b"), newNode(t, dir, bus, "x")
	tlog.SetCommittee([][]byte{a.sy.pub(), b.sy.pub()})
	t.Cleanup(func() { tlog.SetCommittee(nil) })
	past := time.Now().Unix() - 120
	a.register(t, "a@nym", past)
	b.register(t, "b@nym", past+1)
	x.register(t, "x@nym", past+2)
	all := []*node{a, b, x}
	for round := 0; round < 2; round++ {
		for _, n := range all {
			n.sync(t)
		}
	}
	log, _ := checkpoint.CoverHeads(a.st.Entries, checkpoint.ChainHeads(a.st.Entries))
	for _, k := range checkpoint.Issuers(log) {
		if bytes.Equal(k, x.sy.pub()) {
			t.Fatal("outsider is an issuer")
		}
	}
	if r := checkpoint.Rank(log, x.sy.pub()); r != -1 {
		t.Fatalf("outsider rank %d", r)
	}
	issued := 0
	for _, n := range all {
		cp, err := n.sy.IssueCheckpoint()
		dieOnT(t, err)
		if cp != nil {
			issued++
			if bytes.Equal(cp.PubKey, x.sy.pub()) {
				t.Fatal("outsider issued")
			}
		}
	}
	if issued != 1 {
		t.Fatalf("%d issued", issued)
	}
	for round := 0; round < 3; round++ {
		for _, n := range all {
			n.sync(t)
		}
	}
	good := a.st.LatestCheckpoint()
	if good == nil || len(good.Payload.Comm) != 2 {
		t.Fatal("no allowlisted checkpoint")
	}

	// x and an accomplice y forge a takeover of a@nym: an earlier
	// register by x plus a "checkpoint" of their own covering it.
	_, ypriv, _ := ed25519.GenerateKey(nil)
	steal, _ := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: "a@nym", DID: "did:x",
		TS: past - 10, Nonce: []byte{7}}, x.sy.Priv, x.st.Tip(x.sy.pub()))
	x.st.AppendEntry(steal)
	forged, _ := tlog.New(tlog.Payload{Kind: tlog.KindCheckpoint, TS: time.Now().Unix(), Root: "r", Count: 1,
		Comm:  [][]byte{x.sy.pub(), ypriv.Public().(ed25519.PublicKey)},
		Heads: []string{steal.HashHex()}, Nonce: []byte{8}}, x.sy.Priv, steal.Hash)
	cs, _ := tlog.CoSign(forged, ypriv)
	forged.CoSigs = []tlog.Cosig{cs}
	x.st.AppendEntry(forged)
	for round := 0; round < 2; round++ {
		for _, n := range all {
			n.sync(t)
		}
	}
	if !bytes.Equal(a.st.LatestCheckpoint().Hash, good.Hash) {
		t.Fatal("forged checkpoint became the latest")
	}
	for _, n := range all {
		if !bytes.Equal(n.st.Ownership("a@nym").Owner, a.sy.pub()) {
			t.Fatal("forged checkpoint finalised a takeover")
		}
	}
	if handshake.TrustedCheckpoint(forged) {
		t.Fatal("forged checkpoint trusted")
	}
	if rep := checkpoint.Audit(a.st.Entries); rep.OffCommittee != 1 || rep.Diverged {
		t.Fatalf("audit: off=%d diverged=%v", rep.OffCommittee, rep.Diverged)
	}
}
