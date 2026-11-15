package tlog

import (
	"crypto/ed25519"
	"testing"
)

// FuzzDecodeVerify: decoding and verifying arbitrary bytes must never
// panic, and nothing but a correctly signed entry may verify.
func FuzzDecodeVerify(f *testing.F) {
	priv := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	e, _ := New(Payload{Kind: KindRegister, Handle: "a@nym", DID: "did:key:a", TS: 1}, priv, nil)
	good, _ := e.Encode()
	f.Add(good)
	cp, _ := New(Payload{Kind: KindCheckpoint, TS: 1, Comm: [][]byte{priv.Public().(ed25519.PublicKey)}}, priv, nil)
	b, _ := cp.Encode()
	f.Add(b)
	f.Add([]byte(`{"p":{"k":3,"cm":[""]},"pk":"","x":[{"p":"","s":""}]}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		e, err := Decode(raw)
		if err != nil || e == nil {
			return
		}
		if e.Verify() == nil && !ed25519.Verify(ed25519.PublicKey(e.PubKey), mustSB(e), e.Sig) {
			t.Fatal("verified without a valid signature")
		}
		_ = e.HashHex()
		_, _ = e.Encode()
	})
}

func mustSB(e *Entry) []byte { b, _ := e.SigningBytes(); return b }
