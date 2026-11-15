package tlog

import (
	"crypto/ed25519"
	"testing"
)

// A memoised pass must not vouch for a tampered copy of the entry.
func TestVerifyCacheRejectsTampering(t *testing.T) {
	priv := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	e, err := New(Payload{Kind: KindRegister, Handle: "a@nym", DID: "did:key:a", TS: 1}, priv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Verify(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(x *Entry){
		"did":   func(x *Entry) { x.Payload.DID = "did:key:b" },
		"sig":   func(x *Entry) { x.Sig = append([]byte(nil), x.Sig...); x.Sig[0] ^= 1 },
		"hash":  func(x *Entry) { x.Hash = append([]byte(nil), x.Hash...); x.Hash[0] ^= 1 },
		"cosig": func(x *Entry) { x.CoSigs = []Cosig{{Pub: x.PubKey, Sig: x.Sig}} },
	} {
		c := *e
		mut(&c)
		if c.Verify() == nil {
			t.Errorf("%s tampered entry verified", name)
		}
	}
	if err := e.Verify(); err != nil {
		t.Fatalf("original stopped verifying: %v", err)
	}
}

func TestVerifyCacheConcurrent(t *testing.T) {
	priv := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	var es []*Entry
	for i := 0; i < 64; i++ {
		e, err := New(Payload{Kind: KindRegister, Handle: "a@nym", DID: "did:key:a", TS: int64(i + 1)}, priv, nil)
		if err != nil {
			t.Fatal(err)
		}
		es = append(es, e)
	}
	done := make(chan error, 8)
	for g := 0; g < 8; g++ {
		go func() {
			for r := 0; r < 20; r++ {
				for _, e := range es {
					if err := e.Verify(); err != nil {
						done <- err
						return
					}
				}
			}
			done <- nil
		}()
	}
	for g := 0; g < 8; g++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
