package checkpoint

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/ao3575911/telegraph/internal/merkle"
	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

type Report struct {
	Total, SigOK                          int
	SigBad                                []string
	Orphans                               []string
	ChForks                               []string
	NoCheckpoint                          bool
	CP                                    *tlog.Entry
	ClaimedRoot                           string
	ClaimedCount                          int64
	CoveredRoot                           string
	CoveredCount                          int
	CoveredHashes                         map[string]bool
	RootOk, CountOk, CommitteeOk, ChainOk bool
	ChainGaps                             []string
	Uncovered                             int
	Late                                  int // uncovered but stamped before the checkpoint (delivered late)
	Missing                               int // covered chain links absent locally (sync incomplete)
	PendingCP                             int // checkpoints still awaiting co-signatures
	OffCommittee                          int // checkpoints whose committee is outside TELEGRAPH_COMMITTEE (ignored)
	Diverged                              bool
}

func Audit(entries []tlog.Entry) Report {
	rep := Report{Total: len(entries), CoveredHashes: map[string]bool{}}
	var valid, logEntries, cps []tlog.Entry
	for i := range entries {
		e := entries[i]
		if err := e.Verify(); err != nil {
			if tlog.Unattested(err) {
				// well-formed, awaiting co-signatures or off the committee
				// allowlist: part of its issuer's chain, attests nothing
				if errors.Is(err, tlog.ErrOffCommittee) {
					rep.OffCommittee++
				} else {
					rep.PendingCP++
				}
				valid = append(valid, e)
				continue
			}
			rep.SigBad = append(rep.SigBad, fmt.Sprintf("%s: %v", short(e.HashHex()), err))
			continue
		}
		valid = append(valid, e)
		if IsCP(e) {
			cps = append(cps, e)
		} else if e.Payload.Kind != tlog.KindHeartbeat {
			logEntries = append(logEntries, e)
		}
	}
	rep.SigOK = len(valid) - rep.PendingCP - rep.OffCommittee
	auditChains(valid, &rep)

	if len(cps) == 0 {
		rep.NoCheckpoint = true
		return rep
	}
	var latest *tlog.Entry
	for i := range cps {
		e := cps[i]
		if latest == nil || e.Payload.TS > latest.Payload.TS ||
			(e.Payload.TS == latest.Payload.TS && bytes.Compare(e.Hash, latest.Hash) > 0) {
			e := e
			latest = &e
		}
	}
	rep.CP = latest
	rep.ClaimedRoot = latest.Payload.Root
	rep.ClaimedCount = latest.Payload.Count

	covered, missing := CoveredBy(latest, valid)
	rep.Missing = missing
	rep.CoveredCount = len(covered)
	rep.CoveredRoot = merkle.Root(Hashes(covered))
	for i := range covered {
		rep.CoveredHashes[covered[i].HashHex()] = true
	}
	rep.Uncovered = len(logEntries) - len(covered)
	if len(latest.Payload.Heads) > 0 {
		for i := range logEntries {
			if !rep.CoveredHashes[logEntries[i].HashHex()] && logEntries[i].Payload.TS < latest.Payload.TS {
				rep.Late++
			}
		}
	}

	rep.RootOk = rep.CoveredRoot == rep.ClaimedRoot
	rep.CountOk = int64(rep.CoveredCount) == rep.ClaimedCount
	want := SelectCommittee(Target(rep.ClaimedRoot, rep.ClaimedCount), Issuers(covered))
	rep.CommitteeOk = commEqual(want, latest.Payload.Comm)
	rep.ChainOk, rep.ChainGaps = checkCPChain(cps)
	rep.Diverged = !(rep.RootOk && rep.CountOk && rep.CommitteeOk && rep.ChainOk)
	return rep
}

func commEqual(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([][]byte(nil), a...)
	bs := append([][]byte(nil), b...)
	sort.Slice(as, func(i, j int) bool { return bytes.Compare(as[i], as[j]) < 0 })
	sort.Slice(bs, func(i, j int) bool { return bytes.Compare(bs[i], bs[j]) < 0 })
	for i := range as {
		if !bytes.Equal(as[i], bs[i]) {
			return false
		}
	}
	return true
}

func auditChains(es []tlog.Entry, rep *Report) {
	byIssuer := map[string][]tlog.Entry{}
	for i := range es {
		iss := hex.EncodeToString(es[i].PubKey)
		byIssuer[iss] = append(byIssuer[iss], es[i])
	}
	gen := hex.EncodeToString(tlog.Genesis[:])
	for iss, es := range byIssuer {
		byPrev := map[string][]tlog.Entry{}
		var roots []tlog.Entry
		for i := range es {
			ph := hex.EncodeToString(es[i].PrevHash)
			if ph == gen {
				roots = append(roots, es[i])
			}
			byPrev[ph] = append(byPrev[ph], es[i])
		}
		if len(roots) == 0 {
			rep.Orphans = append(rep.Orphans, fmt.Sprintf("%s: no genesis entry", short(iss)))
			continue
		}
		if len(roots) > 1 {
			rep.ChForks = append(rep.ChForks, fmt.Sprintf("%s: %d genesis entries", short(iss), len(roots)))
		}
		seen := map[string]bool{roots[0].HashHex(): true}
		cur := roots[0]
		for {
			next := byPrev[cur.HashHex()]
			if len(next) == 0 {
				break
			}
			if len(next) > 1 {
				rep.ChForks = append(rep.ChForks,
					fmt.Sprintf("%s: fork after %s", short(iss), short(cur.HashHex())))
			}
			cur = lowestHash(next)
			seen[cur.HashHex()] = true
		}
		if len(seen) != len(es) {
			rep.Orphans = append(rep.Orphans,
				fmt.Sprintf("%s: %d/%d unreachable from genesis", short(iss), len(es)-len(seen), len(es)))
		}
	}
}

func lowestHash(es []tlog.Entry) tlog.Entry {
	best := es[0]
	for _, e := range es[1:] {
		if bytes.Compare(e.Hash, best.Hash) < 0 {
			best = e
		}
	}
	return best
}

// checkCPChain: cps sorted by TS/Hash. Missing PrevCP references and
// mid-chain duplicates are hard failures (fork); sibling branches from a
// common parent are recorded as gaps (fork suspicion, resolves on
// union checkpoint).
func checkCPChain(cps []tlog.Entry) (bool, []string) {
	sorted := append([]tlog.Entry(nil), cps...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Payload.TS != sorted[j].Payload.TS {
			return sorted[i].Payload.TS < sorted[j].Payload.TS
		}
		return bytes.Compare(sorted[i].Hash, sorted[j].Hash) < 0
	})
	have := map[string]bool{}
	for i := range sorted {
		have[sorted[i].HashHex()] = true
	}
	var gaps []string
	hardFail := false
	if len(sorted) > 0 && sorted[0].Payload.PrevCP != "" {
		gaps = append(gaps, fmt.Sprintf(
			"cp chain head references withheld checkpoint %s (gap suspected)",
			short(sorted[0].Payload.PrevCP)))
	}
	for i := 1; i < len(sorted); i++ {
		want := sorted[i].Payload.PrevCP
		switch {
		case want == "":
			gaps = append(gaps, fmt.Sprintf("cp#%d: empty PrevCP mid-chain", i))
		case want == sorted[i-1].HashHex():
			// clean link
		default:
			if have[want] {
				gaps = append(gaps, fmt.Sprintf("cp#%d: branch at %s (prior sibling %s)",
					i, short(want), short(sorted[i-1].HashHex())))
			} else {
				hardFail = true
				gaps = append(gaps, fmt.Sprintf("cp#%d: PrevCP %s missing from log",
					i, short(want)))
			}
		}
	}
	// Sibling branches are a fork until a later checkpoint supersedes
	// every other branch tip. The latest checkpoint's root is recomputed
	// over all log entries up to its timestamp (see Audit), so a strictly
	// newer tip that covers at least as many entries attests to the union.
	if n := len(sorted); n > 1 {
		latest := sorted[n-1]
		hasChild := map[string]bool{}
		for i := range sorted {
			hasChild[sorted[i].Payload.PrevCP] = true
		}
		for i := 0; i < n-1; i++ {
			tip := sorted[i]
			if hasChild[tip.HashHex()] {
				continue
			}
			if tip.Payload.TS < latest.Payload.TS && tip.Payload.Count <= latest.Payload.Count {
				continue
			}
			hardFail = true
			gaps = append(gaps, fmt.Sprintf("unresolved fork: branch tip %s not superseded by %s",
				short(tip.HashHex()), short(latest.HashHex())))
		}
	}
	return !hardFail, gaps
}

// ---- pin rotation ----

// ChainTo walks fromCP's PrevCP links backwards; true if it reaches
// wantHex (or wantHex == "" at a genesis link).
func ChainTo(entries []tlog.Entry, fromCP, wantHex string) (bool, int) {
	byHash := map[string]*tlog.Entry{}
	for i := range entries {
		if IsCP(entries[i]) && entries[i].Verify() == nil {
			e := entries[i]
			byHash[e.HashHex()] = &e
		}
	}
	cur, ok := byHash[fromCP]
	if !ok {
		return false, 0
	}
	depth := 1
	for {
		if cur.HashHex() == wantHex || (wantHex == "" && cur.Payload.PrevCP == "") {
			return true, depth
		}
		if cur.Payload.PrevCP == "" || depth > 10000 {
			return false, depth
		}
		cur, ok = byHash[cur.Payload.PrevCP]
		if !ok {
			return false, depth
		}
		depth++
	}
}

func ancestors(entries []tlog.Entry, cpHex string) map[string]bool {
	byHash := map[string]*tlog.Entry{}
	for i := range entries {
		if IsCP(entries[i]) && entries[i].Verify() == nil {
			e := entries[i]
			byHash[e.HashHex()] = &e
		}
	}
	out := map[string]bool{}
	cur := cpHex
	for cur != "" {
		e, ok := byHash[cur]
		if !ok {
			break
		}
		out[cur] = true
		cur = e.Payload.PrevCP
	}
	return out
}

// SharesAncestry: two cps descend from a common checkpoint. Used for
// partition healing — both side-pins are causal branches of one genesis.
func SharesAncestry(entries []tlog.Entry, a, b string) bool {
	aa, bb := ancestors(entries, a), ancestors(entries, b)
	for k := range aa {
		if bb[k] {
			return true
		}
	}
	return false
}

func RotatePin(entries []tlog.Entry, oldPin string) (string, string) {
	latest := latestValidCP(entries)
	if latest == nil {
		return "", "no valid latest checkpoint"
	}
	if latest.HashHex() == oldPin {
		return oldPin, ""
	}
	if ok, _ := ChainTo(entries, latest.HashHex(), oldPin); ok {
		return latest.HashHex(), ""
	}
	// partition-heal path: old pin is a valid sibling branch sharing
	// ancestry with the new tip (which itself must be fully quorumed).
	old := findByHash(entries, oldPin)
	if old != nil && old.Verify() == nil && SharesAncestry(entries, latest.HashHex(), oldPin) {
		return latest.HashHex(), ""
	}
	return "", fmt.Sprintf("latest cp %s does not chain back to pin %s",
		short(latest.HashHex()), short(oldPin))
}

func latestValidCP(entries []tlog.Entry) *tlog.Entry {
	var best *tlog.Entry
	for i := range entries {
		e := &entries[i]
		if !IsCP(*e) || e.Verify() != nil {
			continue
		}
		if best == nil || e.Payload.TS > best.Payload.TS ||
			(e.Payload.TS == best.Payload.TS && bytes.Compare(e.Hash, best.Hash) > 0) {
			best = e
		}
	}
	return best
}

func findByHash(entries []tlog.Entry, hashHex string) *tlog.Entry {
	for i := range entries {
		if entries[i].HashHex() == hashHex {
			return &entries[i]
		}
	}
	return nil
}

// DumpVerdict: bootstrap admission. Pin present + audit clean.
func DumpVerdict(staged []tlog.Entry, pinHex string) (ok bool, reason string) {
	if pinHex == "" {
		rep := Audit(staged)
		if rep.Diverged {
			return false, "staged log diverges (no pin set)"
		}
		return true, ""
	}
	found := false
	for i := range staged {
		if staged[i].Payload.Kind == tlog.KindCheckpoint && staged[i].HashHex() == pinHex {
			found = true
			break
		}
	}
	if !found {
		return false, fmt.Sprintf(
			"pinned checkpoint %s absent from dump (hostile or stale source)", short(pinHex))
	}
	rep := Audit(staged)
	if rep.Diverged {
		return false, "dump contains pinned cp but diverges from it"
	}
	return true, ""
}

// ---- net stats ----

type NetStats struct {
	Pinned       string
	PinAge       int64
	LastCPAge    int64
	PeerTotal    int
	PeerLive     int
	PeerDead     int
	PinDrift     int
	PinDriftPct  int
	DriftedPins  []string
	LastCPChains bool
	Health       string
}

func GatherStats(st *store.Store, now int64) NetStats {
	ns := NetStats{Pinned: st.Pin, PinAge: -1, LastCPAge: -1}
	if st.PinSetAt > 0 {
		ns.PinAge = now - st.PinSetAt
	}
	if cp := latestValidCP(st.Entries); cp != nil {
		ns.LastCPAge = now - cp.Payload.TS
		ns.LastCPChains, _ = ChainTo(st.Entries, cp.HashHex(), st.Pin)
	}
	foreign := map[string]bool{}
	pinCount := 0
	for k, p := range st.Peers {
		ns.PeerTotal++
		age := now - p.LastSeen
		switch {
		case age < 12*3600:
			ns.PeerLive++
			for i := range st.Entries {
				e := &st.Entries[i]
				if e.KeyHex() == k && e.Payload.Kind == tlog.KindHeartbeat &&
					e.HashHex() == p.LastHB {
					if e.Payload.Pin != "" && e.Payload.Pin != st.Pin {
						pinCount++
						foreign[e.Payload.Pin] = true
					}
				}
			}
		case age > 48*3600:
			ns.PeerDead++
		}
	}
	ns.PinDrift = pinCount
	for f := range foreign {
		ns.DriftedPins = append(ns.DriftedPins, short(f))
	}
	sort.Strings(ns.DriftedPins)
	if ns.PeerLive > 0 {
		ns.PinDriftPct = 100 * ns.PinDrift / ns.PeerLive
	}
	switch {
	case ns.Pinned == "":
		ns.Health = "UNPINNED"
	case ns.LastCPAge < 0 || ns.LastCPAge > 7*24*3600:
		ns.Health = "STALE"
	case ns.PinDriftPct >= 51:
		ns.Health = "DRIFTED"
	case st.Pin != "" && !ns.LastCPChains:
		ns.Health = "BROKEN"
	default:
		ns.Health = "OK"
	}
	return ns
}

func PeerStatus(st *store.Store, pubHex string, now int64) string {
	p := st.Peers[pubHex]
	if p == nil {
		return "unknown"
	}
	age := now - p.LastSeen
	live := "stale"
	if age < 12*3600 {
		live = "live"
	} else if age > 48*3600 {
		live = "dead"
	}
	rep := Audit(st.Entries)
	if rep.NoCheckpoint {
		return live + "/unattested"
	}
	for i := range st.Entries {
		e := &st.Entries[i]
		if e.KeyHex() == pubHex && e.Payload.Kind == tlog.KindRegister &&
			rep.CoveredHashes[e.HashHex()] {
			return live + "/attested"
		}
	}
	return live + "/uncovered"
}

func short(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
