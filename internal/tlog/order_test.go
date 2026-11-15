package tlog

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	mrand "math/rand"
	"testing"
)

// world builds signers chains of length n, all in one second, with forks.
func world(t *testing.T, rng *mrand.Rand, signers, n int) []Entry {
	t.Helper()
	var all []Entry
	for s := 0; s < signers; s++ {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		var mine [][]byte
		for i := 0; i < n; i++ {
			var prev []byte
			if len(mine) > 0 {
				prev = mine[rng.Intn(len(mine))] // forks off any earlier entry
				if rng.Intn(3) > 0 {
					prev = mine[len(mine)-1]
				}
			}
			e, err := New(Payload{Kind: KindRegister, Handle: "h@nym", TS: 42, Nonce: []byte{byte(s), byte(i)}}, priv, prev)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, *e)
			mine = append(mine, e.Hash)
		}
	}
	return all
}

func ptrs(es []Entry) []*Entry {
	out := make([]*Entry, len(es))
	for i := range es {
		out[i] = &es[i]
	}
	return out
}

// TestSortCausalPermutations: same-second entries come out in one order
// for every input permutation, and every entry follows its ancestors.
func TestSortCausalPermutations(t *testing.T) {
	for seed := int64(0); seed < 100; seed++ {
		rng := mrand.New(mrand.NewSource(seed))
		all := world(t, rng, 1+rng.Intn(4), 1+rng.Intn(12))
		want := ptrs(all)
		SortCausal(want, all)
		pos := map[string]int{}
		for i, e := range want {
			pos[string(e.Hash)] = i
		}
		for _, e := range want {
			if p, ok := pos[string(e.PrevHash)]; ok && p > pos[string(e.Hash)] {
				t.Fatalf("seed %d: entry sorted before its parent", seed)
			}
		}
		for perm := 0; perm < 30; perm++ {
			in := append([]Entry(nil), all...)
			rng.Shuffle(len(in), func(i, j int) { in[i], in[j] = in[j], in[i] })
			got := ptrs(in)
			SortCausal(got, in)
			for i := range got {
				if !bytes.Equal(got[i].Hash, want[i].Hash) {
					t.Fatalf("seed %d perm %d: order depends on input order", seed, perm)
				}
			}
		}
	}
}

// TestSortCausalTSFirst: timestamps still dominate chain order, so a
// back-dated child still sorts by its own TS.
func TestSortCausalTSFirst(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	a, _ := New(Payload{Kind: KindRegister, Handle: "h@nym", TS: 10}, priv, nil)
	b, _ := New(Payload{Kind: KindRegister, Handle: "h@nym", TS: 9}, priv, a.Hash)
	all := []Entry{*a, *b}
	es := ptrs(all)
	SortCausal(es, all)
	if es[0].Payload.TS != 9 {
		t.Fatal("TS must order before chain")
	}
}

// TestSortCausalLongChain: depth is walked iteratively.
func TestSortCausalLongChain(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	var all []Entry
	var prev []byte
	for i := 0; i < 20000; i++ {
		e, _ := New(Payload{Kind: KindHeartbeat, Handle: "hb", TS: 7, Nonce: []byte{byte(i), byte(i >> 8)}}, priv, prev)
		all = append(all, *e)
		prev = e.Hash
	}
	es := ptrs(all)
	mrand.New(mrand.NewSource(1)).Shuffle(len(es), func(i, j int) { es[i], es[j] = es[j], es[i] })
	SortCausal(es, all)
	for i := range es {
		if !bytes.Equal(es[i].Hash, all[i].Hash) {
			t.Fatalf("chain order broken at %d", i)
		}
	}
}
