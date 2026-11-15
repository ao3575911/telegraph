package checkpoint

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/ao3575911/telegraph/internal/merkle"
	"github.com/ao3575911/telegraph/internal/tlog"
)

var Interval = 6 * time.Hour

const MinNewEntries = 8

func IsCP(e tlog.Entry) bool { return e.Payload.Kind == tlog.KindCheckpoint }

func Target(rootHex string, count int64) []byte {
	h := sha256.Sum256([]byte(fmt.Sprintf("telegraph/cp/%s/%d", rootHex, count)))
	return h[:]
}

// RingDistance is the shortest distance between pub and target on the
// 2^(8n) ring, min((pub-target), (target-pub)) mod 2^(8n), big-endian and
// fixed width n = max(len(pub), len(target)). It is symmetric, so "closest"
// means closest in either direction, including across the wrap point.
func RingDistance(pub, target []byte) []byte {
	n := len(pub)
	if len(target) > n {
		n = len(target)
	}
	a, b := leftPad(pub, n), leftPad(target, n)
	d1, d2 := ringSub(a, b), ringSub(b, a)
	if bytes.Compare(d1, d2) <= 0 {
		return d1
	}
	return d2
}

func leftPad(b []byte, n int) []byte {
	out := make([]byte, n)
	copy(out[n-len(b):], b)
	return out
}

// ringSub returns (a - b) mod 2^(8*len(a)); a and b have equal length.
func ringSub(a, b []byte) []byte {
	d := make([]byte, len(a))
	borrow := 0
	for i := len(a) - 1; i >= 0; i-- {
		v := int(a[i]) - int(b[i]) - borrow
		if v < 0 {
			v += 256
			borrow = 1
		} else {
			borrow = 0
		}
		d[i] = byte(v)
	}
	return d
}

// SelectCommittee: the 3 issuers closest to target on the ring, by
// distinct key. Grinding one nonce moves the target randomly — never
// hierarchically — and one key can hold at most one slot.
func SelectCommittee(target []byte, pubs [][]byte) [][]byte {
	seen := map[string]bool{}
	type cand struct{ pub, dist []byte }
	var cs []cand
	for _, p := range pubs {
		if seen[string(p)] {
			continue
		}
		seen[string(p)] = true
		cs = append(cs, cand{p, RingDistance(p, target)})
	}
	sort.Slice(cs, func(i, j int) bool {
		if c := bytes.Compare(cs[i].dist, cs[j].dist); c != 0 {
			return c < 0
		}
		return bytes.Compare(cs[i].pub, cs[j].pub) < 0
	})
	n := 3
	if len(cs) < n {
		n = len(cs)
	}
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		out[i] = cs[i].pub
	}
	return out
}

func InCommittee(comm [][]byte, pub []byte) bool {
	for _, c := range comm {
		if bytes.Equal(c, pub) {
			return true
		}
	}
	return false
}

// Issuers returns, sorted, the keys eligible for a committee over es:
// signers of at least one log entry (not a checkpoint or heartbeat) and,
// when TELEGRAPH_COMMITTEE is set, on that allowlist.
func Issuers(es []tlog.Entry) [][]byte {
	set := map[string][]byte{}
	for i := range es {
		if IsCP(es[i]) || es[i].Payload.Kind == tlog.KindHeartbeat || !tlog.CommitteeAllows(es[i].PubKey) {
			continue
		}
		set[hex.EncodeToString(es[i].PubKey)] = es[i].PubKey
	}
	out := make([][]byte, 0, len(set))
	for _, p := range set {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i], out[j]) < 0 })
	return out
}

func Hashes(es []tlog.Entry) []string {
	out := make([]string, 0, len(es))
	for i := range es {
		out = append(out, es[i].HashHex())
	}
	return out
}

// Build constructs a checkpoint signed by one committee member. ts is
// explicit so tests control ordering deterministically.
func Build(rootHex string, count int64, comm [][]byte, prevCP string, ts int64,
	priv ed25519.PrivateKey, prevHash []byte) (*tlog.Entry, error) {
	nonce := make([]byte, 16)
	rand.Read(nonce)
	return tlog.New(tlog.Payload{
		Kind: tlog.KindCheckpoint, TS: ts,
		Root: rootHex, Count: count, Comm: comm, PrevCP: prevCP, Nonce: nonce,
	}, priv, prevHash)
}

// Covered returns the entries a checkpoint at ts covers: those with a
// timestamp strictly before ts. Entries stamped in the checkpoint's own
// second are left for the next one, so the issuer and every auditor
// derive the same set even when entries keep arriving within that second.
func Covered(log []tlog.Entry, ts int64) []tlog.Entry {
	var out []tlog.Entry
	for i := range log {
		if log[i].Payload.TS < ts {
			out = append(out, log[i])
		}
	}
	return out
}

// BuildAt builds a checkpoint at ts over every issuer chain in entries
// (see ChainHeads) when this node's committee rank is between 0 and
// maxRank. Rank 0 is the leader; ranks 1 and 2 issue only as fallbacks
// (see Due). It returns (nil, rank, nil) when it is not this node's turn.
func BuildAt(entries []tlog.Entry, prevCPHex string, ts int64, maxRank int,
	priv ed25519.PrivateKey, prevHash []byte) (*tlog.Entry, int, error) {
	heads := ChainHeads(entries)
	log, _ := CoverHeads(entries, heads)
	if len(log) == 0 {
		return nil, -1, nil
	}
	pub := []byte(priv.Public().(ed25519.PublicKey))
	rank := Rank(log, pub)
	if rank < 0 || rank > maxRank {
		return nil, rank, nil
	}
	root := merkle.Root(Hashes(log))
	comm := SelectCommittee(Target(root, int64(len(log))), Issuers(log))
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, rank, err
	}
	cp, err := tlog.New(tlog.Payload{
		Kind: tlog.KindCheckpoint, TS: ts, Root: root, Count: int64(len(log)),
		Comm: comm, PrevCP: prevCPHex, Heads: heads, Nonce: nonce,
	}, priv, prevHash)
	return cp, rank, err
}
