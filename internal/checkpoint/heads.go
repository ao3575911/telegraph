package checkpoint

import (
	"encoding/hex"
	"sort"
	"time"

	"github.com/ao3575911/telegraph/internal/merkle"
	"github.com/ao3575911/telegraph/internal/tlog"
)

// Since v0.2.0 a checkpoint names the head of every issuer chain it
// covers (Payload.Heads). The covered set is each chain from genesis to
// its head, so an entry that reaches an auditor but not the issuer is
// simply uncovered instead of breaking the root. Checkpoints without
// Heads keep the v0.1.0 rule: entries stamped before the checkpoint.

// ChainHeads returns, sorted, the hash of the furthest entry on each
// issuer's chain reachable from genesis. At a fork the lowest hash is
// followed, as in the chain audit.
func ChainHeads(entries []tlog.Entry) []string {
	byIssuer := map[string][]tlog.Entry{}
	for i := range entries {
		k := entries[i].KeyHex()
		byIssuer[k] = append(byIssuer[k], entries[i])
	}
	gen := hex.EncodeToString(tlog.Genesis[:])
	var heads []string
	for _, es := range byIssuer {
		byPrev := map[string][]tlog.Entry{}
		for i := range es {
			byPrev[es[i].PrevHex()] = append(byPrev[es[i].PrevHex()], es[i])
		}
		roots := byPrev[gen]
		if len(roots) == 0 {
			continue
		}
		cur := lowestHash(roots)
		seen := map[string]bool{}
		for !seen[cur.HashHex()] {
			seen[cur.HashHex()] = true
			next := byPrev[cur.HashHex()]
			if len(next) == 0 {
				break
			}
			cur = lowestHash(next)
		}
		heads = append(heads, cur.HashHex())
	}
	sort.Strings(heads)
	return heads
}

// CoverHeads returns the log entries (not checkpoints or heartbeats) on
// the chains ending at heads, and how many heads or links are missing
// from entries.
func CoverHeads(entries []tlog.Entry, heads []string) (covered []tlog.Entry, missing int) {
	byHash := make(map[string]*tlog.Entry, len(entries))
	for i := range entries {
		byHash[entries[i].HashHex()] = &entries[i]
	}
	gen := hex.EncodeToString(tlog.Genesis[:])
	taken := map[string]bool{}
	for _, h := range heads {
		cur := h
		for cur != gen && !taken[cur] {
			e, ok := byHash[cur]
			if !ok {
				missing++
				break
			}
			taken[cur] = true
			if k := e.Payload.Kind; k != tlog.KindCheckpoint && k != tlog.KindHeartbeat {
				covered = append(covered, *e)
			}
			cur = e.PrevHex()
		}
	}
	return covered, missing
}

// CoveredBy returns the log entries cp covers within entries, and how
// many covered entries are missing locally (always 0 for v0.1.0
// checkpoints, which cover by timestamp).
func CoveredBy(cp *tlog.Entry, entries []tlog.Entry) ([]tlog.Entry, int) {
	if len(cp.Payload.Heads) > 0 {
		return CoverHeads(entries, cp.Payload.Heads)
	}
	var log []tlog.Entry
	for i := range entries {
		if k := entries[i].Payload.Kind; k != tlog.KindCheckpoint && k != tlog.KindHeartbeat {
			log = append(log, entries[i])
		}
	}
	return Covered(log, cp.Payload.TS), 0
}

// FallbackGrace is how long each committee member after the leader waits
// beyond Interval before issuing in the leader's place.
var FallbackGrace = time.Hour

// Rank is pub's position in the committee for log: 0 for the leader,
// 1 and 2 for the fallbacks, -1 when pub is not on the committee.
func Rank(log []tlog.Entry, pub []byte) int {
	if len(log) == 0 {
		return -1
	}
	comm := SelectCommittee(Target(merkle.Root(Hashes(log)), int64(len(log))), Issuers(log))
	for i, c := range comm {
		if string(c) == string(pub) {
			return i
		}
	}
	return -1
}

// Due reports whether the member at rank should issue now, given how
// long the oldest uncheckpointed entry has waited (stale seconds). The
// leader is due after Interval; each fallback FallbackGrace later.
func Due(rank int, stale int64) bool {
	if rank < 0 {
		return false
	}
	wait := Interval + time.Duration(rank)*FallbackGrace
	return stale >= int64(wait/time.Second)
}

// Staleness is how many seconds the oldest log entry not covered by the
// latest checkpoint has waited at now; 0 when everything is covered.
func Staleness(entries []tlog.Entry, latest *tlog.Entry, now int64) int64 {
	covered := map[string]bool{}
	if latest != nil {
		cs, _ := CoveredBy(latest, entries)
		for i := range cs {
			covered[cs[i].HashHex()] = true
		}
	}
	oldest := int64(-1)
	for i := range entries {
		e := &entries[i]
		if k := e.Payload.Kind; k == tlog.KindCheckpoint || k == tlog.KindHeartbeat || covered[e.HashHex()] {
			continue
		}
		if oldest < 0 || e.Payload.TS < oldest {
			oldest = e.Payload.TS
		}
	}
	if oldest < 0 || now < oldest {
		return 0
	}
	return now - oldest
}
