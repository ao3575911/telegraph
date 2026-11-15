package transport

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/checkpoint"
)

// Issue #3: with the committee leader offline, the first fallback member
// issues once the leader is overdue, the second co-signs, and the
// leader accepts the result when it returns.
func TestFallbackIssuerWhenLeaderOffline(t *testing.T) {
	dir := t.TempDir()
	bus := filepath.Join(dir, "bus.jsonl")
	nodes := []*node{newNode(t, dir, bus, "a"), newNode(t, dir, bus, "b"), newNode(t, dir, bus, "c")}
	wall := time.Now().Unix()
	iv := int64(checkpoint.Interval / time.Second)
	g := int64(checkpoint.FallbackGrace / time.Second)
	past := wall - iv - g - g/2 // oldest entry is Interval + 1.5 grace old
	for i, h := range []string{"alice@nym", "bob@nym", "carol@nym"} {
		nodes[i].register(t, h, past+int64(i))
	}
	for round := 0; round < 2; round++ {
		for _, n := range nodes {
			n.sync(t)
		}
	}
	byRank := map[int]*node{}
	log := nodes[0].st.LogEntries()
	for _, n := range nodes {
		byRank[checkpoint.Rank(log, n.sy.pub())] = n
	}
	leader, first, second := byRank[0], byRank[1], byRank[2]
	if leader == nil || first == nil || second == nil {
		t.Fatalf("ranks not 0,1,2: %v", byRank)
	}

	// Before Interval + grace, the first fallback waits.
	first.sy.Now = func() int64 { return past + iv + g - 10 }
	if cp, rank, err := first.sy.IssueCheckpointRank(false); err != nil || cp != nil || rank != 1 {
		t.Fatalf("early fallback: cp=%v rank=%d err=%v", cp != nil, rank, err)
	}
	// The second fallback is not yet due at Interval + 1.5 grace.
	second.sy.Now = func() int64 { return wall }
	if cp, _, err := second.sy.IssueCheckpointRank(false); err != nil || cp != nil {
		t.Fatalf("second fallback issued early: cp=%v err=%v", cp != nil, err)
	}
	// Now the first fallback is due and issues.
	first.sy.Now = func() int64 { return wall }
	cp, rank, err := first.sy.IssueCheckpointRank(false)
	if err != nil || cp == nil || rank != 1 {
		t.Fatalf("fallback did not issue: cp=%v rank=%d err=%v", cp != nil, rank, err)
	}
	// The serving loop's rule agrees.
	if cp2, _, err := second.sy.IssueCheckpointRank(true); err != nil || cp2 != nil {
		t.Fatalf("serving second fallback issued a sibling: cp=%v err=%v", cp2 != nil, err)
	}

	for round := 0; round < 2; round++ {
		first.sync(t)
		second.sync(t)
	}
	for _, n := range []*node{first, second} {
		rep := checkpoint.Audit(n.st.Entries)
		if rep.NoCheckpoint || rep.Diverged || rep.PendingCP != 0 {
			t.Fatalf("fallback checkpoint: cp=%v diverged=%v pending=%d", !rep.NoCheckpoint, rep.Diverged, rep.PendingCP)
		}
	}

	// The leader returns: it accepts the checkpoint and does not fork it.
	leader.sync(t)
	if rep := checkpoint.Audit(leader.st.Entries); rep.NoCheckpoint || rep.Diverged {
		t.Fatalf("leader rejects fallback checkpoint: diverged=%v", rep.Diverged)
	}
	if _, err := leader.sy.IssueCheckpoint(); !errors.Is(err, ErrAlreadyCheckpointed) {
		t.Fatalf("leader re-checkpointed a covered log: %v", err)
	}
}
