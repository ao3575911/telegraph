package checkpoint

import (
	"errors"
	"testing"

	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

// quorateCP: two actors each register, one issues a checkpoint over both
// and the other co-signs it.
func quorateCP(t *testing.T) (a, b actor, es []tlog.Entry, cp *tlog.Entry) {
	t.Helper()
	a, b = actorNew(t), actorNew(t)
	es = []tlog.Entry{a.reg(t, "a@nym", "did:a", 100), b.reg(t, "b@nym", "did:b", 101)}
	for _, issuer := range []actor{a, b} {
		var tip []byte
		for i := range es {
			if string(es[i].PubKey) == string(issuer.pub) {
				tip = es[i].Hash
			}
		}
		c, _, err := BuildAt(es, "", 200, 2, issuer.priv, tip)
		dieOn2(t, err)
		if c != nil {
			cp = c
			break
		}
	}
	if cp == nil || len(cp.Payload.Comm) != 2 {
		t.Fatal("no two-member checkpoint")
	}
	co := a
	if string(cp.PubKey) == string(a.pub) {
		co = b
	}
	cs, err := tlog.CoSign(cp, co.priv)
	dieOn2(t, err)
	cp.CoSigs = append(cp.CoSigs, cs)
	return a, b, append(es, *cp), cp
}

func storeOf(es []tlog.Entry) *store.Store {
	st := &store.Store{Tips: map[string]string{}, Seen: map[string]bool{}, Peers: map[string]*store.Peer{}}
	for i := range es {
		st.AppendReceived(&es[i])
	}
	return st
}

// The README order (init, register, sync) must not skip the pin check: a
// store holding only its own register still needs --pin (then
// bootstraps) or --genesis.
func TestCheckTrustRegisterFirst(t *testing.T) {
	_, _, net, cp := quorateCP(t)
	me := actorNew(t)
	own := me.reg(t, "me@nym", "did:me", 300)
	st := storeOf([]tlog.Entry{own})
	if _, _, err := CheckTrust(st, me.pub, "", false); !errors.Is(err, ErrNoPin) {
		t.Fatalf("no pin: %v", err)
	}
	if tr, _, err := CheckTrust(st, me.pub, cp.HashHex(), false); err != nil || tr != TrustBootstrap {
		t.Fatalf("own entries only + pin must bootstrap: %v %v", tr, err)
	}
	// already merged unchecked gossip without the pin: refuse
	st = storeOf(append([]tlog.Entry{own}, net[:2]...))
	if _, _, err := CheckTrust(st, me.pub, cp.HashHex(), false); !errors.Is(err, ErrPinNotInLog) {
		t.Fatalf("foreign entries, pin absent: %v", err)
	}
	// holding the pinned checkpoint and auditing clean: OK
	st = storeOf(append([]tlog.Entry{own}, net...))
	if tr, _, err := CheckTrust(st, me.pub, cp.HashHex(), false); err != nil || tr != TrustOK {
		t.Fatalf("pinned log: %v %v", tr, err)
	}
	if _, _, err := CheckTrust(st, me.pub, "00"+cp.HashHex()[2:], false); !errors.Is(err, ErrPinNotInLog) {
		t.Fatalf("wrong pin: %v", err)
	}
}

func TestCheckTrustUnderSignedPin(t *testing.T) {
	a, _, net, cp := quorateCP(t)
	half := *cp
	half.CoSigs = nil
	es := append(append([]tlog.Entry(nil), net[:2]...), half)
	if _, _, err := CheckTrust(storeOf(es), a.pub, cp.HashHex(), false); !errors.Is(err, ErrPinNotVerified) {
		t.Fatalf("under-signed pin accepted: %v", err)
	}
}

func TestCheckTrustGenesis(t *testing.T) {
	a, _, net, cp := quorateCP(t)
	if tr, _, err := CheckTrust(storeOf(net[:2]), a.pub, "", true); err != nil || tr != TrustGenesis {
		t.Fatalf("pre-checkpoint genesis: %v %v", tr, err)
	}
	tr, pin, err := CheckTrust(storeOf(net), a.pub, "", true)
	if err != nil || tr != TrustAdopted || pin != cp.HashHex() {
		t.Fatalf("committee member adopts: %v %q %v", tr, pin, err)
	}
	outsider := actorNew(t)
	if _, _, err := CheckTrust(storeOf(net), outsider.pub, "", true); !errors.Is(err, ErrGenesisHasCP) {
		t.Fatalf("outsider --genesis past genesis: %v", err)
	}
}
