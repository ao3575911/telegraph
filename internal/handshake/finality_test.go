package handshake

import (
	"bytes"
	"testing"

	"github.com/ao3575911/telegraph/internal/tlog"
)

// trustedCP builds a checkpoint by s, co-signed by co, naming heads.
func trustedCP(t *testing.T, s, co act, ts int64, prev []byte, heads ...*tlog.Entry) tlog.Entry {
	t.Helper()
	var hs []string
	for _, h := range heads {
		hs = append(hs, h.HashHex())
	}
	cp, err := tlog.New(tlog.Payload{Kind: tlog.KindCheckpoint, TS: ts, Root: "r", Count: int64(len(hs)),
		Comm: [][]byte{s.pub, co.pub}, Heads: hs, Nonce: []byte{byte(ts)}}, s.priv, prev)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := tlog.CoSign(cp, co.priv)
	if err != nil {
		t.Fatal(err)
	}
	cp.CoSigs = append(cp.CoSigs, cs)
	return *cp
}

func perms(es []tlog.Entry) [][]tlog.Entry {
	if len(es) <= 1 {
		return [][]tlog.Entry{append([]tlog.Entry(nil), es...)}
	}
	var out [][]tlog.Entry
	for i := range es {
		rest := append(append([]tlog.Entry(nil), es[:i]...), es[i+1:]...)
		for _, p := range perms(rest) {
			out = append(out, append([]tlog.Entry{es[i]}, p...))
		}
	}
	return out
}

func ownerAll(t *testing.T, es []tlog.Entry) []byte {
	t.Helper()
	var want []byte
	for i, p := range perms(es) {
		got, _, _ := Owner("x@nym", p)
		if i > 0 && !bytes.Equal(got, want) {
			t.Fatal("owner depends on entry order")
		}
		want = got
	}
	return want
}

func TestConcurrentRegistersEarliestWins(t *testing.T) {
	A, B := newAct(t), newAct(t)
	es := []tlog.Entry{
		plainEntry(t, A, tlog.KindRegister, "x@nym", "did:a", 100),
		plainEntry(t, B, tlog.KindRegister, "x@nym", "did:b", 105),
	}
	if !bytes.Equal(ownerAll(t, es), A.pub) {
		t.Fatal("earliest register lost")
	}
	// same second: hash order decides, identically everywhere
	es[1] = plainEntry(t, B, tlog.KindRegister, "x@nym", "did:b", 100)
	ownerAll(t, es)
}

// A trusted checkpoint finalises the register it covers: an uncovered
// register from another key dated before it is void, and a later
// checkpoint that covers both does not undo that.
func TestCheckpointFinalisesRegister(t *testing.T) {
	A, B, C := newAct(t), newAct(t), newAct(t)
	ra := plainEntry(t, A, tlog.KindRegister, "x@nym", "did:a", 100)
	rb := plainEntry(t, B, tlog.KindRegister, "x@nym", "did:b", 105)
	cp1 := trustedCP(t, C, B, 200, nil, &rb)
	es := []tlog.Entry{ra, rb, cp1}
	if !bytes.Equal(ownerAll(t, es), B.pub) {
		t.Fatal("finalised register lost to an uncovered earlier one")
	}
	cp2 := trustedCP(t, C, B, 300, cp1.Hash, &ra, &rb)
	if p, _, _ := Owner("x@nym", append(es, cp2)); !bytes.Equal(p, B.pub) {
		t.Fatal("a later checkpoint undid finality")
	}
	// an uncovered register dated after the final one is void anyway
	late := plainEntry(t, A, tlog.KindRegister, "x@nym", "did:a", 150)
	if p, _, _ := Owner("x@nym", []tlog.Entry{rb, cp1, late}); !bytes.Equal(p, B.pub) {
		t.Fatal("late register took a finalised handle")
	}
}

// A one-member or under-signed checkpoint finalises nothing, so a lone
// key cannot self-certify a takeover.
func TestUntrustedCheckpointIgnored(t *testing.T) {
	A, B := newAct(t), newAct(t)
	ra := plainEntry(t, A, tlog.KindRegister, "x@nym", "did:a", 100)
	rb := plainEntry(t, B, tlog.KindRegister, "x@nym", "did:b", 105)
	solo, err := tlog.New(tlog.Payload{Kind: tlog.KindCheckpoint, TS: 200, Root: "r", Count: 1,
		Comm: [][]byte{B.pub}, Heads: []string{rb.HashHex()}, Nonce: []byte{1}}, B.priv, rb.Hash)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := tlog.New(tlog.Payload{Kind: tlog.KindCheckpoint, TS: 201, Root: "r", Count: 1,
		Comm: [][]byte{B.pub, A.pub}, Heads: []string{rb.HashHex()}, Nonce: []byte{2}}, B.priv, solo.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if p, _, _ := Owner("x@nym", []tlog.Entry{ra, rb, *solo, *pending}); !bytes.Equal(p, A.pub) {
		t.Fatal("an untrusted checkpoint finalised a takeover")
	}
}
