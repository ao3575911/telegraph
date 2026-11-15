package tlog

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"
)

func genKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func mkEntry(t *testing.T, priv ed25519.PrivateKey, prev []byte, ts int64) *Entry {
	t.Helper()
	e, err := New(Payload{
		Kind: KindRegister, Handle: "alice@nym", DID: "did:key:z6MkTest",
		TS: ts, Nonce: bytes.Repeat([]byte{1}, 16),
	}, priv, prev)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestSignAndVerify(t *testing.T) {
	_, priv := genKey(t)
	if err := mkEntry(t, priv, nil, time.Now().Unix()).Verify(); err != nil {
		t.Fatalf("fresh entry failed verify: %v", err)
	}
}

func TestChainLinksPrevHash(t *testing.T) {
	_, priv := genKey(t)
	e1 := mkEntry(t, priv, nil, time.Now().Unix())
	if !bytes.Equal(e1.PrevHash, Genesis[:]) {
		t.Fatal("genesis entry must have zero prev hash")
	}
	e2 := mkEntry(t, priv, e1.Hash, time.Now().Unix())
	if err := e2.Verify(); err != nil {
		t.Fatalf("chained entry failed verify: %v", err)
	}
	if !bytes.Equal(e2.PrevHash, e1.Hash) {
		t.Fatal("e2 prev hash does not link e1")
	}
}

func TestTamperedPayload(t *testing.T) {
	_, priv := genKey(t)
	e := mkEntry(t, priv, nil, time.Now().Unix())
	b, _ := e.Encode()
	e2, _ := Decode(b)
	e2.Payload.DID = "did:key:z6MkEvil"
	if err := e2.Verify(); err != ErrBadHash {
		t.Fatalf("want ErrBadHash, got %v", err)
	}
}

func TestForgedSignature(t *testing.T) {
	_, attacker := genKey(t)
	b, _ := mkEntry(t, attacker, nil, time.Now().Unix()).Encode()
	e, _ := Decode(b)
	victim, _ := genKey(t)
	e.PubKey = victim
	e.Hash = e.ComputeHash()
	if err := e.Verify(); err != ErrBadSig {
		t.Fatalf("want ErrBadSig, got %v", err)
	}
}

func TestRoundtrip(t *testing.T) {
	_, priv := genKey(t)
	e := mkEntry(t, priv, nil, time.Now().Unix())
	b, _ := e.Encode()
	d, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if d.HashHex() != e.HashHex() {
		t.Fatal("hash changed across roundtrip")
	}
}

func TestFutureTSRejected(t *testing.T) {
	_, priv := genKey(t)
	e := mkEntry(t, priv, nil, time.Now().Unix()+25*3600)
	if err := e.Verify(); err == nil {
		t.Fatal("far-future entry accepted")
	}
	e2 := mkEntry(t, priv, nil, time.Now().Unix()-365*24*3600)
	if err := e2.Verify(); err != nil {
		t.Fatalf("old entry rejected — history must stay valid: %v", err)
	}
}

func TestVerifyAtDeterministic(t *testing.T) {
	_, priv := genKey(t)
	now := int64(5000000000)
	e := mkEntry(t, priv, nil, now-100)
	if err := VerifyAt(e, now); err != nil {
		t.Fatalf("valid-at-time entry rejected: %v", err)
	}
	if err := VerifyAt(e, now-26*3600); err != ErrFutureTS {
		t.Fatalf("want ErrFutureTS, got %v", err)
	}
}

func TestCoSignAttachesValidly(t *testing.T) {
	_, priv := genKey(t)
	_, priv2 := genKey(t)
	e := mkEntry(t, priv, nil, time.Now().Unix())
	cs, err := CoSign(e, priv2)
	if err != nil {
		t.Fatal(err)
	}
	e.CoSigs = []Cosig{cs}
	e.Hash = e.ComputeHash()
	// non-checkpoints must REJECT stray co-sigs (kind confusion guard)
	if err := e.verifyRaw(); err == nil {
		t.Fatal("register entry with co-sigs accepted")
	}
}

var _ = json.Marshal
