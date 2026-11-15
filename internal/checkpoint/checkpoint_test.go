package checkpoint

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	mrand "math/rand"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/merkle"
	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

type actor struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func actorNew(t *testing.T) actor {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return actor{pub, priv}
}

func (a actor) reg(t *testing.T, handle, did string, ts int64) tlog.Entry {
	t.Helper()
	nonce := make([]byte, 16)
	rand.Read(nonce)
	e, err := tlog.New(tlog.Payload{
		Kind: tlog.KindRegister, Handle: handle, DID: did, TS: ts, Nonce: nonce,
	}, a.priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	return *e
}

func shuffle(es []tlog.Entry) []tlog.Entry {
	out := append([]tlog.Entry(nil), es...)
	mrand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func hbFrom(t *testing.T, a actor, pin string, ts int64) tlog.Entry {
	t.Helper()
	e, err := tlog.New(tlog.Payload{
		Kind: tlog.KindHeartbeat, Handle: "hb", DID: "alive",
		TS: ts, Pin: pin, Nonce: []byte{1},
	}, a.priv, nil)
	dieOn2(t, err)
	return *e
}

func dieOn2(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// cpFrom: 2-of-3 signed checkpoint over log, chained to prevCP.
// Signer selection: first two committee members able to sign.
func cpFrom(t *testing.T, log []tlog.Entry, prevCP string, ts int64, acts ...actor) tlog.Entry {
	t.Helper()
	root := merkle.Root(Hashes(log))
	comm := SelectCommittee(Target(root, int64(len(log))), Issuers(log))
	byPub := map[string]actor{}
	for _, a := range acts {
		byPub[hex.EncodeToString(a.pub)] = a
	}
	var builders []actor
	for _, c := range comm {
		if a, ok := byPub[hex.EncodeToString(c)]; ok {
			builders = append(builders, a)
		}
	}
	// Quorum per tlog.verifyCheckpoint: RequiredCPSigs when the committee
	// has more than one member, otherwise the lone member's signature.
	need := tlog.RequiredCPSigs
	if len(comm) < need {
		need = len(comm)
	}
	if len(builders) < need {
		t.Fatalf("only %d committee signers among actors (need %d)", len(builders), need)
	}
	cp, err := Build(root, int64(len(log)), comm, prevCP, ts, builders[0].priv, nil)
	dieOn2(t, err)
	for _, b := range builders[1:need] {
		cs, err := tlog.CoSign(cp, b.priv)
		dieOn2(t, err)
		cp.CoSigs = append(cp.CoSigs, cs)
	}
	cp.Hash = cp.ComputeHash()
	return *cp
}

func TestMerkleOrderIndependence(t *testing.T) {
	hs := []string{"aa11", "bb22", "cc33", "dd44", "ee55"}
	a := merkle.Root(hs)
	for i := 0; i < 5; i++ {
		sh := append([]string(nil), hs...)
		mrand.Shuffle(len(sh), func(x, y int) { sh[x], sh[y] = sh[y], sh[x] })
		if b := merkle.Root(sh); b != a {
			t.Fatal("root depends on order")
		}
	}
}

func TestNotaryWraparound(t *testing.T) {
	pubs := [][]byte{{0x01}, {0x50}, {0xF0}}
	if c := SelectCommittee([]byte{0x80}, pubs); !bytes.Equal(c[0], []byte{0x50}) {
		t.Fatal("ring order wrong")
	}
	if c := SelectCommittee([]byte{0xFF}, pubs); !contains(c, []byte{0x01}) {
		t.Fatal("wraparound must include lowest key")
	}
}

func contains(cs [][]byte, want []byte) bool {
	for _, c := range cs {
		if bytes.Equal(c, want) {
			return true
		}
	}
	return false
}

func TestAuditGreenPath(t *testing.T) {
	A, B, C := actorNew(t), actorNew(t), actorNew(t)
	log := []tlog.Entry{
		A.reg(t, "alice@nym", "did:A", 1000),
		B.reg(t, "bob@nym", "did:B", 1100),
		C.reg(t, "carol@nym", "did:C", 1200),
	}
	cp := cpFrom(t, log, "", 2000, A, B, C)
	rep := Audit(shuffle(append(log, cp)))
	if rep.Diverged {
		t.Fatalf("honest log diverged: root=%v count=%v comm=%v chain=%v",
			rep.RootOk, rep.CountOk, rep.CommitteeOk, rep.ChainOk)
	}
}

func TestAuditCatchesSplice(t *testing.T) {
	A, B, C := actorNew(t), actorNew(t), actorNew(t)
	log := []tlog.Entry{
		A.reg(t, "alice@nym", "did:A", 1000),
		B.reg(t, "bob@nym", "did:B", 1100),
	}
	cp := cpFrom(t, log, "", 2000, A, B, C)
	E := actorNew(t)
	splice := E.reg(t, "alice@nym", "did:EVIL", 1050)
	rep := Audit(shuffle(append(append(log, cp), splice)))
	if !rep.Diverged {
		t.Fatal("splice undetected")
	}
}

func TestSoloSigRejected(t *testing.T) {
	A, B, C := actorNew(t), actorNew(t), actorNew(t)
	log := []tlog.Entry{A.reg(t, "a@nym", "did:A", 1000), B.reg(t, "b@nym", "did:B", 1100)}
	root := merkle.Root(Hashes(log))
	comm := SelectCommittee(Target(root, 2), Issuers(log))
	cp, err := Build(root, 2, comm, "", 2000, C.priv, nil) // C not on committee
	dieOn2(t, err)
	if cp.Verify() == nil {
		t.Fatal("non-member checkpoint sig accepted")
	}
}

func TestGrinding10kNeverMajority(t *testing.T) {
	// A fresh random issuer layout per trial: with one fixed layout the
	// attacker's share depends on its arc width, not on grinding, and the
	// 0.35–0.65 band fails on roughly half of all runs.
	issuers := make([][]byte, 5)
	inComm, maxSlots := 0, 0
	for i := 0; i < 10000; i++ {
		for j := range issuers {
			issuers[j] = make([]byte, 32)
			rand.Read(issuers[j])
		}
		attacker := issuers[0]
		nonce := make([]byte, 32)
		rand.Read(nonce)
		root := hex.EncodeToString(nonce)
		comm := SelectCommittee(Target(root, int64(i%50)), issuers)
		slots := 0
		for _, c := range comm {
			if bytes.Equal(c, attacker) {
				slots++
			}
		}
		if slots > maxSlots {
			maxSlots = slots
		}
		if slots > 0 {
			inComm++
		}
	}
	if maxSlots > 1 {
		t.Fatalf("grinder held %d committee slots", maxSlots)
	}
	frac := float64(inComm) / 10000
	if frac < 0.35 || frac > 0.65 { // expected 3/5 = 0.60; wide band
		t.Fatalf("grinding skewed membership to %.2f", frac)
	}
}

func TestChainBreakDetected(t *testing.T) {
	A, B, C := actorNew(t), actorNew(t), actorNew(t)
	log := []tlog.Entry{A.reg(t, "a@nym", "did:A", 1000)}
	cp1 := cpFrom(t, log, "", 2000, A, B, C)
	// orphan link
	cp2 := cpFrom(t, log, "deadbeef", 3000, A, B, C)
	rep := Audit(shuffle(append(append(log, cp1), cp2)))
	if rep.ChainOk {
		t.Fatal("broken PrevCP chain link not reported")
	}
}

// ---- milestone 5: bootstrap / pin ----

func TestDumpVerdictPinEnforced(t *testing.T) {
	A, B, C := actorNew(t), actorNew(t), actorNew(t)
	log := []tlog.Entry{A.reg(t, "a@nym", "did:A", 1000)}
	cp := cpFrom(t, log, "", 2000, A, B, C)
	pin := cp.HashHex()
	ok, _ := DumpVerdict(shuffle(append(log, cp)), pin)
	if !ok {
		t.Fatal("honest dump rejected")
	}
	// hostile: own valid cp, pin absent
	E := actorNew(t)
	hlog := []tlog.Entry{E.reg(t, "a@nym", "did:EVIL", 1050)}
	hcp := cpFrom(t, hlog, "", 2000, E) // E is the only issuer, so the only notary
	ok, reason := DumpVerdict(shuffle(append(hlog, hcp)), pin)
	if ok {
		t.Fatal("hostile dump accepted")
	}
	if !bytes.Contains([]byte(reason), []byte("pinned checkpoint")) {
		t.Fatalf("wrong rejection reason: %s", reason)
	}
}

// ---- milestone 6: rotation + partition ----

func TestRotateRequiresCausality(t *testing.T) {
	A, B, C := actorNew(t), actorNew(t), actorNew(t)
	log := []tlog.Entry{A.reg(t, "a@nym", "did:A", 1000)}
	cp1 := cpFrom(t, log, "", 2000, A, B, C)
	log2 := append(append([]tlog.Entry{}, log...), C.reg(t, "c@nym", "did:C", 2500))
	cp2 := cpFrom(t, log2, cp1.HashHex(), 3000, A, B, C)

	newPin, reason := RotatePin(append(log2, cp1, cp2), cp1.HashHex())
	if reason != "" || newPin != cp2.HashHex() {
		t.Fatalf("honest rotate failed: %s", reason)
	}
	rogue := cpFrom(t, []tlog.Entry{C.reg(t, "x@nym", "did:X", 1500)}, "", 1500, A, B, C)
	if pin, reason := RotatePin(append(log2, cp1, cp2, rogue), cp1.HashHex()); reason == "" && pin != cp2.HashHex() {
		t.Fatal("rotate advanced past a non-causal rogue")
	}
}

func TestPartitionHeal(t *testing.T) {
	A, B, C := actorNew(t), actorNew(t), actorNew(t)
	base := []tlog.Entry{A.reg(t, "a@nym", "did:A", 1000), B.reg(t, "b@nym", "did:B", 1100)}
	cp1 := cpFrom(t, base, "", 2000, A, B, C)
	pinG := cp1.HashHex()

	sideA2 := append(append([]tlog.Entry{}, base...), A.reg(t, "a2@nym", "did:A2", 2200))
	cpA := cpFrom(t, sideA2, pinG, 3000, A, B, C)
	sideB := append(append([]tlog.Entry{}, base...), B.reg(t, "b2@nym", "did:B2", 2300))
	cpB := cpFrom(t, sideB, pinG, 3000, A, B, C)

	merged := append(append(append([]tlog.Entry{}, sideA2...), sideB...), cp1, cpA, cpB)
	rep := Audit(merged)
	if rep.ChainOk {
		t.Fatal("partition branches not detected")
	}
	// union heal: quorum over the union, chained to cp1
	union := append(append([]tlog.Entry{}, sideA2...), sideB...)
	union = append(union, cp1)
	cpUnion := cpFrom(t, filterSwapKinds(union), cp1.HashHex(), 4000, A, B, C)
	all := append(merged, cpUnion)
	pinA, r1 := RotatePin(all, cpA.HashHex())
	pinB, r2 := RotatePin(all, cpB.HashHex())
	if r1 != "" || r2 != "" {
		t.Fatalf("heal rotates refused: %s / %s", r1, r2)
	}
	if pinA != pinB {
		t.Fatal("sides converged on different pins")
	}
	hbs := []tlog.Entry{hbFrom(t, A, pinA, 4100), hbFrom(t, B, pinA, 4100)}
	rep2 := Audit(append(all, hbs...))
	if rep2.Diverged {
		t.Fatalf("post-heal diverged: %v %v %v %v", rep2.RootOk, rep2.CountOk, rep2.CommitteeOk, rep2.ChainOk)
	}
}

func filterSwapKinds(es []tlog.Entry) []tlog.Entry {
	var out []tlog.Entry
	for i := range es {
		k := es[i].Payload.Kind
		if k != tlog.KindCheckpoint && k != tlog.KindHeartbeat {
			out = append(out, es[i])
		}
	}
	return out
}

func TestNetStatsDrift(t *testing.T) {
	A, B, C := actorNew(t), actorNew(t), actorNew(t)
	base := []tlog.Entry{A.reg(t, "a@nym", "did:A", 1000)}
	cp1 := cpFrom(t, base, "", 2000, A, B, C)
	now := time.Now().Unix()
	st := &store.Store{Tips: map[string]string{}, Seen: map[string]bool{}, Peers: map[string]*store.Peer{}}
	st.SetPin(cp1.HashHex())
	entries := []tlog.Entry{
		hbFrom(t, A, cp1.HashHex(), now-3600),
		hbFrom(t, B, cp1.HashHex(), now-3600),
		hbFrom(t, C, "d2cafe00", now-3600), // C drifted
	}
	entries = append(entries, base...)
	entries = append(entries, cp1)
	for i := range entries {
		st.AppendEntry(&entries[i])
	}
	ns := GatherStats(st, now)
	if ns.PeerLive != 3 || ns.PinDriftPct != 33 {
		t.Fatalf("peers=%d drift=%d%%", ns.PeerLive, ns.PinDriftPct)
	}
	if ns.Health == "DRIFTED" {
		t.Fatal("33% drift is not a majority")
	}
}
