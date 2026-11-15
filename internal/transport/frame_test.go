package transport

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	mrand "math/rand"
	"testing"
	"time"

	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

func randData(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func TestSplitAssembleRoundtrip(t *testing.T) {
	for _, n := range []int{10, MaxChunk, MaxChunk + 1, MaxChunk*3 + 777} {
		data := randData(t, n)
		frames := SplitFrames("a1b2c3d4", data)
		asm := NewAssembler()
		var got []byte
		done := false
		for _, fr := range frames {
			out, _, ok, err := asm.Add(fr)
			dieOnT(t, err)
			if ok {
				got, done = out, true
			}
		}
		if !done || !bytes.Equal(got, data) {
			t.Fatalf("n=%d roundtrip failed", n)
		}
	}
}

func TestShuffledDelivery(t *testing.T) {
	data := randData(t, MaxChunk*2)
	frames := SplitFrames("d1e2f3a4", data)
	mrand.Shuffle(len(frames), func(i, j int) { frames[i], frames[j] = frames[j], frames[i] })
	asm := NewAssembler()
	var got []byte
	done := false
	for _, fr := range frames {
		if out, _, ok, _ := asm.Add(fr); ok {
			got, done = out, true
		}
	}
	if !done || !bytes.Equal(got, data) {
		t.Fatal("out-of-order frames failed reassembly")
	}
}

func TestIncompleteNeverEmits(t *testing.T) {
	data := randData(t, MaxChunk*3)
	frames := SplitFrames("f1e2d3c4", data)
	asm := NewAssembler()
	for _, fr := range frames[:len(frames)-1] {
		if _, _, ok, _ := asm.Add(fr); ok {
			t.Fatal("emitted before completion")
		}
	}
}

func TestDuplicateFramesTolerated(t *testing.T) {
	data := randData(t, MaxChunk+5)
	frames := SplitFrames("aabbccdd", data)
	frames = append(frames, frames...)
	asm := NewAssembler()
	var got []byte
	done := false
	for _, fr := range frames {
		if out, _, ok, _ := asm.Add(fr); ok {
			got, done = out, true
		}
	}
	if !done || !bytes.Equal(got, data) {
		t.Fatal("duplicate delivery broke reassembly")
	}
}

func TestSyncerIngestEndToEnd(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	e, err := tlog.New(tlog.Payload{
		Kind: tlog.KindRegister, Handle: "zoe@nym", DID: "did:key:z6MkZ",
		TS: time.Now().Unix(), Nonce: randData(t, 16),
	}, priv, nil)
	dieOnT(t, err)
	enc, _ := e.Encode()
	frames := SplitFrames(e.HashHex(), enc)

	st := &store.Store{Tips: map[string]string{}, Seen: map[string]bool{}, Peers: map[string]*store.Peer{}}
	sy := NewSyncer(nil, nil, st, priv)
	for _, fr := range frames {
		ok, err := sy.IngestText(fr)
		dieOnT(t, err)
		if ok && len(st.Entries) != 1 {
			t.Fatal("entry not merged on ingest")
		}
	}
	for _, fr := range frames { // replay: idempotent
		sy.IngestText(fr)
	}
	if len(st.Entries) != 1 {
		t.Fatal("replayed gossip duplicated entries")
	}
}

func dieOnT(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
