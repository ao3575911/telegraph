package checkpoint

import (
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/tlog"
)

// cpHeads builds a v0.2.0 checkpoint over entries as the committee
// leader, co-signed by the next member, with acts holding the keys.
func cpHeads(t *testing.T, entries []tlog.Entry, ts int64, acts ...actor) tlog.Entry {
	t.Helper()
	for _, a := range acts {
		cp, rank, err := BuildAt(entries, "", ts, 0, a.priv, nil)
		dieOn2(t, err)
		if cp == nil {
			continue
		}
		if rank != 0 {
			t.Fatalf("rank %d built with maxRank 0", rank)
		}
		for _, b := range acts {
			if b.pub.Equal(a.pub) || !InCommittee(cp.Payload.Comm, b.pub) {
				continue
			}
			cs, err := tlog.CoSign(cp, b.priv)
			dieOn2(t, err)
			cp.CoSigs = append(cp.CoSigs, cs)
			break
		}
		if err := cp.Verify(); err != nil {
			t.Fatalf("checkpoint does not verify: %v", err)
		}
		return *cp
	}
	t.Fatal("no actor leads the committee")
	return tlog.Entry{}
}

// Issue #1: an entry stamped before a checkpoint but delivered to the
// issuer after it must not break the checkpoint for nodes that have it.
func TestLateEntryStaysUncovered(t *testing.T) {
	A, B, C := actorNew(t), actorNew(t), actorNew(t)
	issuerView := []tlog.Entry{
		A.reg(t, "alice@nym", "did:A", 1000),
		B.reg(t, "bob@nym", "did:B", 1100),
	}
	late := C.reg(t, "carol@nym", "did:C", 1200) // stamped before the checkpoint
	cp := cpHeads(t, issuerView, 2000, A, B, C)
	if len(cp.Payload.Heads) != 2 {
		t.Fatalf("heads = %d, want 2", len(cp.Payload.Heads))
	}
	rep := Audit(shuffle(append(append(issuerView, cp), late)))
	if rep.Diverged {
		t.Fatalf("late entry broke the checkpoint: root=%v count=%v comm=%v chain=%v",
			rep.RootOk, rep.CountOk, rep.CommitteeOk, rep.ChainOk)
	}
	if rep.Uncovered != 1 || rep.Late != 1 {
		t.Fatalf("uncovered=%d late=%d, want 1 and 1", rep.Uncovered, rep.Late)
	}
	if rep.CoveredHashes[late.HashHex()] {
		t.Fatal("late entry reported as attested")
	}
}

// The v0.1.0 rule still applies to checkpoints without Heads.
func TestLegacyCheckpointCoversByTimestamp(t *testing.T) {
	A, B, C := actorNew(t), actorNew(t), actorNew(t)
	log := []tlog.Entry{A.reg(t, "a@nym", "did:A", 1000), B.reg(t, "b@nym", "did:B", 1100)}
	cp := cpFrom(t, log, "", 2000, A, B, C)
	if rep := Audit(append(log, cp)); rep.Diverged {
		t.Fatal("legacy checkpoint diverged")
	}
	late := C.reg(t, "c@nym", "did:C", 1200)
	if rep := Audit(append(append(log, cp), late)); !rep.Diverged {
		t.Fatal("legacy checkpoint ignored an entry stamped before it")
	}
}

// Covered entries an auditor lacks are counted, and the root fails.
func TestHeadsMissingEntryDetected(t *testing.T) {
	A, B, C := actorNew(t), actorNew(t), actorNew(t)
	log := []tlog.Entry{
		A.reg(t, "a@nym", "did:A", 1000),
		B.reg(t, "b@nym", "did:B", 1100),
		C.reg(t, "c@nym", "did:C", 1200),
	}
	cp := cpHeads(t, log, 2000, A, B, C)
	rep := Audit([]tlog.Entry{log[0], log[1], cp})
	if rep.Missing != 1 || !rep.Diverged {
		t.Fatalf("missing=%d diverged=%v, want 1 and true", rep.Missing, rep.Diverged)
	}
}

// Heads follow each issuer's chain, including entries past a checkpoint.
func TestChainHeadsFollowChains(t *testing.T) {
	A := actorNew(t)
	e1 := A.reg(t, "a@nym", "did:A", 1000)
	e2, err := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: "a2@nym", DID: "did:A2", TS: 900}, A.priv, e1.Hash)
	dieOn2(t, err)
	heads := ChainHeads([]tlog.Entry{*e2, e1})
	if len(heads) != 1 || heads[0] != e2.HashHex() {
		t.Fatalf("heads = %v, want [%s]", heads, e2.HashHex()[:8])
	}
	cov, missing := CoverHeads([]tlog.Entry{*e2}, heads)
	if len(cov) != 1 || missing != 1 {
		t.Fatalf("covered=%d missing=%d, want 1 and 1", len(cov), missing)
	}
}

// Issue #3: fallback members become due one grace period apart.
func TestFallbackDue(t *testing.T) {
	iv := int64(Interval / time.Second)
	g := int64(FallbackGrace / time.Second)
	cases := []struct {
		rank  int
		stale int64
		want  bool
	}{
		{0, iv - 1, false}, {0, iv, true},
		{1, iv, false}, {1, iv + g, true},
		{2, iv + g, false}, {2, iv + 2*g, true},
		{-1, 1 << 40, false},
	}
	for _, c := range cases {
		if got := Due(c.rank, c.stale); got != c.want {
			t.Errorf("Due(%d, %d) = %v, want %v", c.rank, c.stale, got, c.want)
		}
	}
}

func TestStaleness(t *testing.T) {
	A, B, C := actorNew(t), actorNew(t), actorNew(t)
	log := []tlog.Entry{A.reg(t, "a@nym", "did:A", 1000), B.reg(t, "b@nym", "did:B", 1100)}
	if got := Staleness(log, nil, 5000); got != 4000 {
		t.Fatalf("no checkpoint: staleness %d, want 4000", got)
	}
	cp := cpHeads(t, log, 2000, A, B, C)
	all := append(append([]tlog.Entry(nil), log...), cp)
	if got := Staleness(all, &cp, 5000); got != 0 {
		t.Fatalf("all covered: staleness %d, want 0", got)
	}
	all = append(all, C.reg(t, "c@nym", "did:C", 3000))
	if got := Staleness(all, &cp, 5000); got != 2000 {
		t.Fatalf("one new entry: staleness %d, want 2000", got)
	}
}
