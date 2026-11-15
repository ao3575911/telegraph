package main

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ao3575911/telegraph/internal/checkpoint"
	"github.com/ao3575911/telegraph/internal/handshake"
	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
	"github.com/ao3575911/telegraph/internal/transport"
)

func doInit(dir string) {
	dieOn(os.MkdirAll(dir, 0o700))
	priv, err := loadKey(dir)
	dieOn(err)
	st, err := store.Open(dataPath(dir))
	dieOn(err)
	dieOn(st.Save())
	out.OK("initialised %s", dir)
	out.KV([][2]string{{"key", out.Dim(short(fmt.Sprintf("%x", pubOf(priv))) + "…")}})
}

func appendOwn(st *store.Store, e *tlog.Entry) {
	if !st.AppendEntry(e) {
		die(errors.New("entry rejected by local store"))
	}
	dieOn(st.Save())
}

func doRegister(dir string, args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	handle := fs.String("handle", "", "handle, e.g. alice@nym")
	did := fs.String("did", "", "DID or DID document hash")
	_ = fs.Parse(args)
	if *handle == "" || *did == "" {
		die(errors.New("both --handle and --did are required"))
	}
	dieOn(tlog.ValidHandle(*handle))
	priv := mustKey(dir)
	st := mustStore(dir)
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	e, err := tlog.New(tlog.Payload{
		Kind: tlog.KindRegister, Handle: *handle, DID: *did,
		TS: time.Now().Unix(), Nonce: nonce,
	}, priv, st.Tip(pubOf(priv)))
	dieOn(err)
	appendOwn(st, e)
	out.OK("registered %s", out.Bold(*handle))
	out.KV([][2]string{
		{"did", *did},
		{"owner", short(e.KeyHex())},
		{"entry", out.Dim(short(e.HashHex()))},
	})
}

func doRevoke(dir string, args []string) {
	fs := flag.NewFlagSet("revoke", flag.ExitOnError)
	handle := fs.String("handle", "", "handle to revoke")
	_ = fs.Parse(args)
	if *handle == "" {
		die(errors.New("--handle is required"))
	}
	priv := mustKey(dir)
	st := mustStore(dir)
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	e, err := tlog.New(tlog.Payload{
		Kind: tlog.KindRevoke, Handle: *handle,
		TS: time.Now().Unix(), Nonce: nonce,
	}, priv, st.Tip(pubOf(priv)))
	dieOn(err)
	appendOwn(st, e)
	out.OK("revoked %s", out.Bold(*handle))
	out.KV([][2]string{{"entry", out.Dim(short(e.HashHex()))}})
}

func doResolve(dir, handle string) {
	st := mustStore(dir)
	reg, err := st.Registry()
	if err != nil {
		die(fmt.Errorf("local log failed verification: %w", err))
	}
	e, status, ok := reg.Resolve(handle)
	switch {
	case !ok:
		out.Warn("%s unregistered", out.Bold(handle))
		return
	case e.Payload.Kind == tlog.KindRevoke:
		out.Warn("%s revoked", out.Bold(handle))
		return
	}
	did := e.Payload.DID
	if e.Payload.Kind != tlog.KindRegister {
		did = out.Dim("(no DID yet: new owner has not registered one)")
	}
	out.OK("%s → %s  %s", out.Bold(handle), did, out.Cyan("["+status.String()+"]"))
	out.KV([][2]string{
		{"owner", short(e.KeyHex())},
		{"since", time.Unix(e.Payload.TS, 0).UTC().Format(time.RFC3339)},
		{"entry", out.Dim(short(e.HashHex()))},
	})
}

func doVerify(dir, handle string) {
	st := mustStore(dir)
	reg, err := st.Registry()
	dieOn(err)
	e, status, ok := reg.Resolve(handle)
	if !ok {
		out.Fail("%s UNREGISTERED", out.Bold(handle))
		os.Exit(1)
	}
	rep := checkpoint.Audit(st.Entries)
	head := fmt.Sprintf("%s → %s  %s  %s", out.Bold(handle), e.Payload.DID,
		out.Cyan("["+status.String()+"]"), out.Dim("owner "+short(e.KeyHex())))
	switch {
	case rep.Diverged:
		out.Fail("%s", head)
		out.Fail("DIVERGED: resolution unreliable, run `telegraph audit`")
		os.Exit(2)
	case rep.NoCheckpoint:
		out.Warn("%s", head)
		out.Warn("UNATTESTED: no checkpoint yet")
	case rep.CoveredHashes[e.HashHex()]:
		out.OK("%s", head)
		out.OK("ATTESTED by checkpoint %s", short(rep.CP.HashHex()))
	default:
		out.Warn("%s", head)
		out.Warn("UNCOVERED: newer than checkpoint %s", short(rep.CP.HashHex()))
	}
}

func doAudit(dir string) {
	st := mustStore(dir)
	rep := checkpoint.Audit(st.Entries)
	bad := len(rep.SigBad) > 0 || len(rep.Orphans) > 0
	summary := fmt.Sprintf("audit  %d entries · %d signatures ok · %d bad",
		rep.Total, rep.SigOK, len(rep.SigBad))
	if rep.PendingCP > 0 {
		summary += fmt.Sprintf(" · %d checkpoint awaiting co-signature", rep.PendingCP)
	}
	out.Println(out.Bold(summary))
	for _, b := range rep.SigBad {
		out.Fail("bad signature  %s", b)
	}
	for _, o := range rep.Orphans {
		out.Fail("orphan  %s", o)
	}
	for _, f := range rep.ChForks {
		out.Warn("fork  %s", out.Dim(f))
	}
	if rep.NoCheckpoint {
		out.Warn("no checkpoint yet: history unattested")
		if bad {
			os.Exit(1)
		}
		return
	}
	cp := rep.CP
	rows := [][]string{
		{out.Mark(rep.RootOk), "root", short(rep.CoveredRoot), out.Dim("claimed " + short(rep.ClaimedRoot))},
		{out.Mark(rep.CountOk), "count", fmt.Sprint(rep.CoveredCount), out.Dim(fmt.Sprintf("claimed %d", rep.ClaimedCount))},
		{out.Mark(rep.CommitteeOk), "committee", fmt.Sprintf("%d members", len(cp.Payload.Comm)),
			out.Dim(fmt.Sprintf("%d signatures", 1+len(cp.CoSigs)))},
		{out.Mark(rep.ChainOk), "chain", fmt.Sprintf("%d gaps", len(rep.ChainGaps)), ""},
	}
	out.Printf("  checkpoint %s\n", out.Cyan(short(cp.HashHex())))
	out.Table("  ", rows)
	for _, g := range rep.ChainGaps {
		out.Warn("gap  %s", out.Dim(g))
	}
	if rep.Uncovered > 0 {
		out.Info("%d entries newer than the checkpoint (normal)", rep.Uncovered)
	}
	if rep.Late > 0 {
		out.Info("%d of them stamped before it but delivered late: not attested until the next checkpoint", rep.Late)
	}
	if rep.Missing > 0 {
		out.Warn("%d entries the checkpoint covers have not arrived yet: sync again", rep.Missing)
	}
	switch {
	case rep.Diverged:
		out.Fail("DIVERGED: fork or withholding detected")
	case bad:
		out.Fail("FAILED: bad signatures or orphaned entries")
	default:
		out.OK("OK")
	}
	if rep.Diverged || bad {
		os.Exit(1)
	}
}

func doSync(dir string, args []string) {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	serve := fs.Bool("serve", false, "publish, then poll forever")
	win := fs.Int("secs", 5, "poll window in seconds")
	botsStr := fs.String("bots", envOr("TELEGRAPH_BOTS", ""), "comma-separated bot tokens")
	chansStr := fs.String("channels", envOr("TELEGRAPH_CHANNELS", ""), "comma-separated channels")
	bus := fs.String("bus", envOr("TELEGRAPH_BUS", ""), "local file bus instead of Telegram (testing, demos)")
	pinStr := fs.String("pin", envOr("TELEGRAPH_PIN", ""), "genesis checkpoint pin (first sync)")
	cpOnly := fs.Bool("checkpoint-only", false, "attempt one checkpoint then exit")
	genesis := fs.Bool("genesis", false, "start or join a network that has no checkpoint yet (no pin)")
	botReg := fs.Bool("bot-register", envOr("TELEGRAPH_BOT_REGISTER", "") == "1",
		"let Telegram users /register handles, signed by this node's key (custodial)")
	_ = fs.Parse(args)

	var conns []transport.Conn
	channels := splitCSV(*chansStr)
	if *bus != "" {
		fb, err := transport.OpenFileBus(*bus, filepath.Join(dir, "bus.offset"))
		dieOn(err)
		conns = append(conns, fb)
		if len(channels) == 0 {
			channels = []string{"local"}
		}
	} else {
		tokens := splitCSV(*botsStr)
		if len(tokens) == 0 {
			die(errors.New("no transport: set --bots/TELEGRAPH_BOTS (or --bus/TELEGRAPH_BUS for a local bus)"))
		}
		if len(channels) == 0 {
			die(errors.New("no channels: --channels or TELEGRAPH_CHANNELS required"))
		}
		for _, t := range tokens {
			conns = append(conns, transport.NewBot(t))
		}
	}
	st := mustStore(dir)
	if *genesis && (st.Pin != "" || *pinStr != "") {
		die(errors.New("--genesis is for a network with no checkpoint yet: drop it when you have a pin"))
	}
	if st.Pin == "" && *pinStr != "" && len(st.Entries) == 0 {
		st.SetPin(*pinStr)
		dieOn(st.Save())
	} else if *pinStr != "" && st.Pin != "" && st.Pin != *pinStr {
		die(fmt.Errorf("--pin %s conflicts with local pin %s", short(*pinStr), short(st.Pin)))
	}
	priv := mustKey(dir)
	sy := transport.NewSyncer(conns, channels, st, priv)
	if *bus == "" {
		sy.DM = &transport.Responder{Version: "v" + Version}
		if *botReg {
			sy.DM.Priv = priv
		}
	}

	if *cpOnly {
		cp, rank, err := sy.IssueCheckpointRank(false)
		if errors.Is(err, transport.ErrAlreadyCheckpointed) {
			out.Info("current log already checkpointed")
			return
		}
		dieOn(err)
		if cp == nil {
			switch {
			case rank < 0:
				out.Info("not on the committee for this log: nothing to do")
			default:
				out.Info("not committee leader: will co-sign on sync (fallback #%d if the leader is overdue)", rank)
			}
			return
		}
		need := 1
		if len(cp.Payload.Comm) > 1 {
			need = tlog.RequiredCPSigs
		}
		if rank > 0 {
			out.OK("checkpoint %s issued as fallback #%d (leader overdue)", out.Cyan(short(cp.HashHex())), rank)
		} else {
			out.OK("checkpoint %s issued", out.Cyan(short(cp.HashHex())))
		}
		out.KV([][2]string{
			{"root", short(cp.Payload.Root)},
			{"entries", fmt.Sprint(cp.Payload.Count)},
			{"committee", fmt.Sprintf("%d members · %d of %d signatures", len(cp.Payload.Comm), 1, need)},
		})
		return
	}
	if len(st.Entries) == 0 && !*genesis {
		if st.Pin == "" {
			die(errors.New("empty store: pass --pin <checkpoint-hash> to bootstrap, or --genesis to start or join a network that has no checkpoint yet"))
		}
		out.Info("bootstrapping with pin %s…", short(st.Pin))
		n, err := sy.Bootstrap(*win)
		dieOn(err)
		out.OK("bootstrap complete: %d entries accepted", n)
		return
	}
	if *serve {
		out.Info("serving: publish · poll · checkpoint · heartbeat")
		sy.SetupBots()
		sy.Serve()
		return
	}
	start := time.Now()
	n, err := sy.PublishPending()
	dieOn(err)
	merged, err := sy.PollWindow(*win)
	dieOn(err)
	out.OK("synced  %s published  %s merged  %s", out.Bold(fmt.Sprint(n)),
		out.Bold(fmt.Sprint(merged)), out.Dim(fmt.Sprintf("%d entries · %.1fs", len(st.Entries), time.Since(start).Seconds())))
}

func doPin(dir string, args []string) {
	fs := flag.NewFlagSet("pin", flag.ExitOnError)
	rotate := fs.Bool("rotate", false, "advance pin to the latest quorum-verified checkpoint")
	_ = fs.Parse(args)
	st := mustStore(dir)
	if *rotate {
		if st.Pin == "" {
			die(errors.New("no current pin: set one first (bootstrap or pin)"))
		}
		newPin, reason := checkpoint.RotatePin(st.Entries, st.Pin)
		if reason != "" {
			die(fmt.Errorf("rotate refused: %s", reason))
		}
		if newPin == st.Pin {
			out.OK("pin already at tip %s", short(st.Pin))
			return
		}
		old := st.Pin
		st.SetPin(newPin)
		dieOn(st.Save())
		out.OK("pin rotated %s → %s", short(old), short(newPin))
		return
	}
	if st.Pin == "" {
		cp := st.LatestCheckpoint()
		if cp == nil {
			out.Warn("no quorate checkpoint yet: pin available after the first one")
			return
		}
		st.SetPin(cp.HashHex())
		dieOn(st.Save())
	}
	out.OK("pin %s", out.Cyan(st.Pin))
	out.Info("share out-of-band; fresh nodes pass it to their first sync")
}

func doPeers(dir string) {
	st := mustStore(dir)
	now := time.Now().Unix()
	if len(st.Peers) == 0 {
		out.Info("no known peers yet: peers appear once their heartbeats arrive")
		return
	}
	keys := make([]string, 0, len(st.Peers))
	for k := range st.Peers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := [][]string{{out.Dim("peer"), out.Dim("last seen"), out.Dim("status")}}
	for _, k := range keys {
		p := st.Peers[k]
		s := checkpoint.PeerStatus(st, k, now)
		switch {
		case strings.HasPrefix(s, "live/attested"):
			s = out.Green(s)
		case strings.HasPrefix(s, "dead"):
			s = out.Red(s)
		default:
			s = out.Yellow(s)
		}
		rows = append(rows, []string{short(k), humanDur(now-p.LastSeen) + " ago", s})
	}
	out.Table("  ", rows)
}

func doNet(dir string) {
	st := mustStore(dir)
	ns := checkpoint.GatherStats(st, time.Now().Unix())
	health := ns.Health
	switch health {
	case "OK":
		out.OK("network %s", out.Green(health))
	case "BROKEN", "DRIFTED":
		out.Fail("network %s", out.Red(health))
	default:
		out.Warn("network %s", out.Yellow(health))
	}
	rows := [][2]string{}
	if ns.Pinned == "" {
		rows = append(rows, [2]string{"pin", out.Dim("none")})
	} else {
		rows = append(rows, [2]string{"pin", short(ns.Pinned) + out.Dim(" · set "+humanDur(ns.PinAge)+" ago")})
	}
	if ns.LastCPAge < 0 {
		rows = append(rows, [2]string{"last cp", out.Dim("none")})
	} else {
		v := humanDur(ns.LastCPAge) + " ago"
		if ns.Pinned != "" {
			v += " · chains to pin " + out.Mark(ns.LastCPChains)
		}
		rows = append(rows, [2]string{"last cp", v})
	}
	rows = append(rows,
		[2]string{"peers", fmt.Sprintf("%d · %d live · %d dead", ns.PeerTotal, ns.PeerLive, ns.PeerDead)},
		[2]string{"pin drift", fmt.Sprintf("%d%% of live peers", ns.PinDriftPct)})
	if len(ns.DriftedPins) > 0 {
		rows = append(rows, [2]string{"foreign pins", strings.Join(ns.DriftedPins, ", ")})
	}
	out.KV(rows)
	if ns.Health == "DRIFTED" {
		out.Info("run `telegraph pin --rotate` (or let `sync --serve` self-heal)")
	}
}

func doCard(dir string, args []string) {
	fs := flag.NewFlagSet("card", flag.ExitOnError)
	handle := fs.String("handle", "", "handle to print a card for")
	_ = fs.Parse(args)
	if *handle == "" {
		die(errors.New("--handle is required"))
	}
	st := mustStore(dir)
	reg, err := st.Registry()
	dieOn(err)
	e, _, ok := reg.Resolve(*handle)
	if !ok {
		die(fmt.Errorf("%s is unregistered", *handle))
	}
	line := handshake.CardLine(*handle, &e)
	out.Println(out.Bold(line))
	out.Info("put this line in your bio; sign it with: telegraph attest --line %q", line)
}

func doAttest(dir string, args []string) {
	fs := flag.NewFlagSet("attest", flag.ExitOnError)
	line := fs.String("line", "", "exact card line to attest")
	_ = fs.Parse(args)
	if *line == "" {
		die(errors.New("--line is required (the output of `telegraph card`)"))
	}
	handle, _, ok := handshake.ParseCardLine(*line)
	if !ok {
		die(errors.New("not a card line: expected ⟁ handle@nym · a1b2c3d4"))
	}
	priv := mustKey(dir)
	st := mustStore(dir)
	e, err := handshake.BuildCard(*line, time.Now().Unix(), priv, st.Tip(pubOf(priv)))
	dieOn(err)
	appendOwn(st, e)
	out.OK("attested card for %s", out.Bold(handle))
	out.KV([][2]string{{"entry", out.Dim(short(e.HashHex()))}})
}

func doScan(dir, line string) {
	h, hex8, ok := handshake.ParseCardLine(line)
	if !ok {
		die(errors.New("not a card line"))
	}
	st := mustStore(dir)
	reg, err := st.Registry()
	dieOn(err)
	e, _, ok := reg.Resolve(h)
	switch {
	case !ok:
		out.Fail("%s UNREGISTERED", out.Bold(h))
		os.Exit(1)
	case short(e.HashHex()) != hex8:
		out.Fail("%s STALE: entry is now %s (card superseded?)", out.Bold(h), short(e.HashHex()))
		os.Exit(1)
	case handshake.VerifyCard(line, h, st.Entries):
		out.OK("%s VERIFIED: card attested by the current owner %s", out.Bold(h), out.Dim(short(e.KeyHex())))
	default:
		out.Warn("%s UNATTESTED: entry matches, but the owner has not signed this card", out.Bold(h))
		os.Exit(1)
	}
}
