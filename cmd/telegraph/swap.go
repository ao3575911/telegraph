package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ao3575911/telegraph/internal/handshake"
	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
	"github.com/ao3575911/telegraph/internal/ui"
)

func swapUsage(p *ui.Printer) {
	p.Printf("%s telegraph swap <step> [flags]\n\n", p.Bold("usage:"))
	p.Table("  ", [][]string{
		{p.Cyan("offer --gives H --wants H [--hours 24]"), p.Dim("initiator: offer and lock your handle")},
		{p.Cyan("accept --sid ID"), p.Dim("responder: lock your handle to the initiator")},
		{p.Cyan("claim --sid ID"), p.Dim("take the handle locked to you")},
		{p.Cyan("reclaim --sid ID"), p.Dim("recover your handle after expiry")},
		{p.Cyan("status --sid ID"), p.Dim("show locks, claims and reclaims")},
	})
}

func doSwap(dir string, args []string) {
	if len(args) == 0 {
		swapUsage(errOut)
		os.Exit(2)
	}
	sub, args := args[0], args[1:]
	priv := mustKey(dir)
	pub := pubOf(priv)
	st := mustStore(dir)
	fs := flag.NewFlagSet("swap "+sub, flag.ExitOnError)
	sid := fs.String("sid", "", "swap id from the offer")

	switch sub {
	case "offer":
		gives := fs.String("gives", "", "handle you give")
		wants := fs.String("wants", "", "handle you want")
		hours := fs.Int64("hours", 24, "responder's lock window in hours (yours is this plus 48h)")
		_ = fs.Parse(args)
		if *gives == "" || *wants == "" {
			die(errors.New("--gives and --wants are required"))
		}
		swapOffer(dir, st, priv, pub, *gives, *wants, *hours)
	case "accept", "claim", "reclaim", "status":
		_ = fs.Parse(args)
		if *sid == "" {
			die(errors.New("--sid is required"))
		}
		switch sub {
		case "accept":
			swapAccept(st, priv, pub, *sid)
		case "claim":
			swapClaim(dir, st, priv, pub, *sid)
		case "reclaim":
			swapReclaim(st, priv, pub, *sid)
		default:
			swapStatus(st, *sid)
		}
	default:
		errOut.Fail("unknown swap step %q", sub)
		swapUsage(errOut)
		os.Exit(2)
	}
}

func swapOffer(dir string, st *store.Store, priv []byte, pub []byte, gives, wants string, hours int64) {
	reg, err := st.Registry()
	dieOn(err)
	bEntry, _, ok := reg.Resolve(wants)
	if !ok {
		die(fmt.Errorf("%s is unregistered here: sync first, or the counterparty must register", wants))
	}
	aEntry, _, ok := reg.Resolve(gives)
	if !ok || !bytes.Equal(aEntry.PubKey, pub) {
		die(fmt.Errorf("you don't own %s", gives))
	}
	pre, hash := handshake.NewSecret()
	now := time.Now().Unix()
	terms := handshake.Terms{
		InitGive: gives, InitWant: wants,
		InitPub: pub, RespPub: bEntry.PubKey,
		Expiry: now + hours*3600,
	}
	id := terms.SwapID()
	nonce := make([]byte, 8)
	_, _ = rand.Read(nonce)
	offer, err := tlog.New(tlog.Payload{
		Kind: tlog.KindOffer, Handle: gives, DID: wants,
		SwapID: id, Hash: hash, Expiry: terms.Expiry, TS: st.NextTS(pub, now), Nonce: nonce,
	}, priv, st.Tip(pub))
	dieOn(err)
	appendOwn(st, offer)
	// The give-lock outlives the responder's lock by a full claim window.
	lock, err := handshake.BuildLock(gives, terms, hash, terms.Expiry+handshake.ClaimWindow,
		st.NextTS(pub, now), priv, st.Tip(pub))
	dieOn(err)
	appendOwn(st, lock)
	dieOn(os.MkdirAll(filepath.Join(dir, "preimages"), 0o700))
	dieOn(os.WriteFile(filepath.Join(dir, "preimages", id), []byte(hex.EncodeToString(pre)), 0o600))
	out.OK("offered %s for %s, %s locked", out.Bold(gives), out.Bold(wants), gives)
	out.KV([][2]string{
		{"swap id", id},
		{"hashlock", short(hash)},
		{"expires", time.Unix(terms.Expiry+handshake.ClaimWindow, 0).UTC().Format(time.RFC3339)},
	})
	out.Info("preimage kept locally; sync to gossip the offer")
}

func findOffer(st *store.Store, id string) *tlog.Entry {
	for i := range st.Entries {
		e := &st.Entries[i]
		if e.Payload.Kind == tlog.KindOffer && e.Payload.SwapID == id && e.Verify() == nil {
			return e
		}
	}
	return nil
}

func findLock(st *store.Store, id string, signer []byte, handle string) *tlog.Entry {
	for i := range st.Entries {
		e := &st.Entries[i]
		if e.Payload.Kind == tlog.KindLock && e.Payload.SwapID == id &&
			bytes.Equal(e.PubKey, signer) && (handle == "" || e.Payload.Handle == handle) &&
			e.Verify() == nil {
			return e
		}
	}
	return nil
}

func swapAccept(st *store.Store, priv, pub []byte, id string) {
	offer := findOffer(st, id)
	if offer == nil {
		die(fmt.Errorf("offer %s not found: sync first", short(id)))
	}
	terms := handshake.Terms{
		InitGive: offer.Payload.Handle, InitWant: offer.Payload.DID,
		InitPub: offer.PubKey, RespPub: pub, Expiry: offer.Payload.Expiry,
	}
	if terms.SwapID() != id {
		die(fmt.Errorf("offer %s is not addressed to this key", short(id)))
	}
	reg, err := st.Registry()
	dieOn(err)
	mine, _, ok := reg.Resolve(terms.InitWant)
	if !ok || !bytes.Equal(mine.PubKey, pub) {
		die(fmt.Errorf("you don't own %s", terms.InitWant))
	}
	give := findLock(st, id, offer.PubKey, terms.InitGive)
	if give == nil {
		die(errors.New("initiator's lock missing: sync again"))
	}
	if p, s, _ := handshake.Owner(terms.InitGive, st.Entries); s != handshake.StateLocked || !bytes.Equal(p, offer.PubKey) {
		die(fmt.Errorf("initiator's lock on %s is not in force (%s)", terms.InitGive, s))
	}
	lock, err := handshake.BuildLock(terms.InitWant, terms, offer.Payload.Hash, terms.Expiry,
		st.NextTS(pub, time.Now().Unix()), priv, st.Tip(pub))
	dieOn(err)
	if err := handshake.ValidateStagger(give, lock); err != nil {
		die(err)
	}
	appendOwn(st, lock)
	out.OK("accepted swap %s: %s locked to the initiator", short(id), out.Bold(terms.InitWant))
	out.KV([][2]string{
		{"you get", terms.InitGive},
		{"stagger", out.Green("✓") + out.Dim(" your lock expires 48h before theirs")},
	})
}

// preimage returns the swap secret: from this node's offer, or as
// revealed publicly by a claim on the other leg.
func preimage(dir string, st *store.Store, id, hash string) ([]byte, error) {
	if raw, err := os.ReadFile(filepath.Join(dir, "preimages", id)); err == nil {
		return hex.DecodeString(string(raw))
	}
	for i := range st.Entries {
		e := &st.Entries[i]
		if e.Payload.Kind != tlog.KindClaim || e.Payload.SwapID != id {
			continue
		}
		sum := sha256.Sum256(e.Payload.Pre)
		if hex.EncodeToString(sum[:]) == hash {
			return e.Payload.Pre, nil
		}
	}
	return nil, errors.New("preimage not revealed yet: wait for the initiator's claim and sync")
}

func swapClaim(dir string, st *store.Store, priv, pub []byte, id string) {
	var lock *tlog.Entry
	for i := range st.Entries {
		e := &st.Entries[i]
		if e.Payload.Kind == tlog.KindLock && e.Payload.SwapID == id &&
			bytes.Equal(e.Payload.Peer, pub) && e.Verify() == nil {
			lock = e
		}
	}
	if lock == nil {
		die(fmt.Errorf("no lock names this key as counterparty for %s: sync first", short(id)))
	}
	pre, err := preimage(dir, st, id, lock.Payload.Hash)
	dieOn(err)
	e, err := handshake.BuildClaim(lock.Payload.Handle, id, pre, st.NextTS(pub, time.Now().Unix()), priv, st.Tip(pub))
	dieOn(err)
	if err := handshake.ValidClaim(lock, e); err != nil {
		die(fmt.Errorf("claim invalid: %v", err))
	}
	appendOwn(st, e)
	out.OK("claimed %s  %s", out.Bold(lock.Payload.Handle), out.Cyan("[SWAPPED]"))
	out.Info("preimage now public; sync to gossip the claim")
}

func swapReclaim(st *store.Store, priv, pub []byte, id string) {
	lock := findLock(st, id, pub, "")
	if lock == nil {
		die(fmt.Errorf("this key locked nothing for %s", short(id)))
	}
	if time.Now().Unix() <= lock.Payload.Expiry {
		die(fmt.Errorf("too early: reclaim after %s",
			time.Unix(lock.Payload.Expiry, 0).UTC().Format(time.RFC3339)))
	}
	e, err := handshake.BuildReclaim(lock.Payload.Handle, id, st.NextTS(pub, time.Now().Unix()), priv, st.Tip(pub))
	dieOn(err)
	appendOwn(st, e)
	out.OK("reclaimed %s  %s", out.Bold(lock.Payload.Handle), out.Cyan("[REVERTED]"))
}

func swapStatus(st *store.Store, id string) {
	rows := [][]string{{out.Dim("step"), out.Dim("handle"), out.Dim("by"), out.Dim("state")}}
	for i := range st.Entries {
		e := &st.Entries[i]
		if e.Payload.SwapID != id {
			continue
		}
		var step string
		switch e.Payload.Kind {
		case tlog.KindOffer:
			step = "offer"
		case tlog.KindLock:
			step = "lock"
		case tlog.KindClaim:
			step = "claim"
		case tlog.KindReclaim:
			step = "reclaim"
		default:
			continue
		}
		state := ""
		if e.Payload.Kind == tlog.KindLock {
			_, s, _ := handshake.Owner(e.Payload.Handle, st.Entries)
			state = s.String()
			if s == handshake.StateOwned {
				state = out.Green(state)
			} else {
				state = out.Yellow(state)
			}
		}
		rows = append(rows, []string{step, e.Payload.Handle, short(e.KeyHex()), state})
	}
	if len(rows) == 1 {
		out.Warn("swap %s: nothing known locally (sync first)", short(id))
		return
	}
	out.Printf("swap %s\n", out.Cyan(short(id)))
	out.Table("  ", rows)
}
