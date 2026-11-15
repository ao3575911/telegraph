// Command stress is a load and scale harness for the Telegraph registry.
// It drives the real packages in memory (no Telegram, no network) and
// prints one measurement per line, so docs/STRESS-REPORT.md can quote
// measured numbers only.
//
//	go run ./cmd/stress -n 100000 -budget 60s -dir /tmp/tgstress
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	mrand "math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ao3575911/telegraph/internal/checkpoint"
	"github.com/ao3575911/telegraph/internal/crdt"
	"github.com/ao3575911/telegraph/internal/handshake"
	"github.com/ao3575911/telegraph/internal/merkle"
	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
	"github.com/ao3575911/telegraph/internal/transport"
)

var (
	n        = flag.Int("n", 1000, "users (one register entry each)")
	budget   = flag.Duration("budget", 60*time.Second, "time budget per phase; slow phases stop early and extrapolate")
	dir      = flag.String("dir", os.TempDir(), "scratch directory for data.json")
	heapCap  = flag.Uint64("heap-gb", 6, "stop generating entries when the Go heap passes this many GiB")
	botUsers = flag.Int("bot-users", 0, "users for the bot /register phase (0: min(n, 2000))")
	gossipN  = flag.Int("gossip-n", 20000, "entries for the FileBus gossip phase")
	phases   = flag.String("phases", "all", "comma list: gen,ingest,save,load,registry,resolve,audit,checkpoint,merge,swap,bot,telegram")
)

func want(p string) bool {
	return *phases == "all" || strings.Contains(","+*phases+",", ","+p+",")
}

func out(phase, metric string, v any) { fmt.Printf("%-10s %-34s %v\n", phase, metric, v) }

func rss() string {
	b, _ := os.ReadFile("/proc/self/status")
	var cur, peak string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "VmRSS:") {
			cur = strings.TrimSpace(l[6:])
		}
		if strings.HasPrefix(l, "VmHWM:") {
			peak = strings.TrimSpace(l[6:])
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return fmt.Sprintf("rss=%s peak=%s heap=%dMiB", cur, peak, ms.HeapAlloc>>20)
}

func heapBytes() uint64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

func pct(ds []time.Duration) string {
	if len(ds) == 0 {
		return "n/a"
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	p := func(q float64) time.Duration { return ds[int(q*float64(len(ds)-1))] }
	return fmt.Sprintf("p50=%v p99=%v max=%v (n=%d)", p(0.50).Round(time.Microsecond),
		p(0.99).Round(time.Microsecond), ds[len(ds)-1].Round(time.Microsecond), len(ds))
}

func rate(k int, d time.Duration) string {
	return fmt.Sprintf("%d in %v = %.0f ops/s", k, d.Round(time.Millisecond), float64(k)/d.Seconds())
}

func key(seed int) ed25519.PrivateKey {
	var s [32]byte
	binary.LittleEndian.PutUint64(s[:], uint64(seed)+1)
	return ed25519.NewKeyFromSeed(s[:])
}

func handle(i int) string { return "u" + strconv.Itoa(i) + "@nym" }

// gen builds one CLI-style register per user, each under its own key, in
// parallel batches. It stops early when the heap passes -heap-gb.
func gen(count int, ts int64) ([]tlog.Entry, string) {
	const batch = 50000
	es := make([]tlog.Entry, 0, count)
	limit := *heapCap << 30
	for lo := 0; lo < count; lo += batch {
		if lo > 0 && heapBytes() > limit {
			return es, fmt.Sprintf("stopped at %d: heap > %d GiB", lo, *heapCap)
		}
		hi := min(lo+batch, count)
		part := make([]tlog.Entry, hi-lo)
		var wg sync.WaitGroup
		w := runtime.NumCPU()
		for k := 0; k < w; k++ {
			wg.Add(1)
			go func(k int) {
				defer wg.Done()
				for i := lo + k; i < hi; i += w {
					e, err := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: handle(i),
						DID: "did:key:z6Mk" + strconv.Itoa(i), TS: ts + int64(i%3600)}, key(i), nil)
					if err != nil {
						panic(err)
					}
					part[i-lo] = *e
				}
			}(k)
		}
		wg.Wait()
		es = append(es, part...)
		if hi%1000000 == 0 {
			out("gen", "progress", fmt.Sprintf("%d (%s)", hi, rss()))
		}
	}
	return es, ""
}

// probe runs f(i) for i in [0,total) until the budget runs out and
// extrapolates the remainder linearly from the last 10% of samples (an
// underestimate when the per-op cost grows with i).
func probe(phase string, total int, f func(i int)) []time.Duration {
	start := time.Now()
	lat := make([]time.Duration, 0, min(total, 1<<20))
	done := 0
	for i := 0; i < total; i++ {
		t := time.Now()
		f(i)
		lat = append(lat, time.Since(t))
		done++
		if time.Since(start) > *budget {
			break
		}
		if done%50000 == 0 && heapBytes() > *heapCap<<30 {
			out(phase, "STOPPED", fmt.Sprintf("heap > %d GiB after %d ops (%s)", *heapCap, done, rss()))
			break
		}
	}
	el := time.Since(start)
	out(phase, "done", rate(done, el))
	if done < total {
		tail := lat[done*9/10:]
		var sum time.Duration
		for _, d := range tail {
			sum += d
		}
		per := sum / time.Duration(len(tail))
		rest := time.Duration(total-done) * per
		out(phase, "BUDGET EXCEEDED", fmt.Sprintf("%d/%d done; tail %v/op; remaining >= %v (EXTRAPOLATED, linear)",
			done, total, per.Round(time.Microsecond), rest.Round(time.Second)))
	}
	out(phase, "latency", pct(append([]time.Duration(nil), lat...)))
	return lat
}

func main() {
	flag.Parse()
	out("env", "cpus", runtime.NumCPU())
	out("env", "go", runtime.Version())
	out("env", "users", *n)
	ts := time.Now().Unix() - 7200

	if *phases == "stream" {
		stream(*n, ts)
		return
	}
	var es []tlog.Entry
	{
		t := time.Now()
		var note string
		es, note = gen(*n, ts)
		el := time.Since(t)
		out("gen", "keygen+sign", rate(len(es), el))
		if note != "" {
			out("gen", "STOPPED", note)
		}
		var bytes int
		frames := 0
		for i := 0; i < len(es) && i < 10000; i++ {
			b, _ := es[i].Encode()
			bytes += len(b)
			frames += len(transport.SplitFrames(es[i].HashHex(), b))
		}
		k := min(len(es), 10000)
		out("gen", "avg entry JSON bytes", bytes/k)
		out("gen", "avg TGP1 frames/entry", float64(frames)/float64(k))
		out("gen", "mem", rss())
	}
	N := len(es)

	if want("ingest") {
		st := &store.Store{Tips: map[string]string{}, Seen: map[string]bool{}, Peers: map[string]*store.Peer{}}
		probe("ingest", N, func(i int) { st.AppendReceived(&es[i]) })
		out("ingest", "mem", rss())
	}

	st := &store.Store{Entries: es, Tips: map[string]string{}, Seen: map[string]bool{}, Peers: map[string]*store.Peer{}}
	for i := range es {
		st.Tips[es[i].KeyHex()] = es[i].HashHex()
		st.Seen[es[i].HashHex()] = true
	}
	path := filepath.Join(*dir, fmt.Sprintf("data-%d.json", N))
	st.Path = path

	if want("save") {
		t := time.Now()
		if err := st.Save(); err != nil {
			out("save", "ERROR", err)
		} else {
			el := time.Since(t)
			fi, _ := os.Stat(path)
			out("save", "full rewrite", el.Round(time.Millisecond))
			out("save", "data.json size", fmt.Sprintf("%d bytes (%.1f MiB, %.0f B/entry)", fi.Size(), float64(fi.Size())/(1<<20), float64(fi.Size())/float64(max(N, 1))))
			out("save", "mem", rss())
		}
	}
	if want("load") {
		runtime.GC()
		t := time.Now()
		st2, err := store.Open(path)
		if err != nil {
			out("load", "ERROR", err)
		} else {
			out("load", "store.Open", fmt.Sprintf("%v (%d entries)", time.Since(t).Round(time.Millisecond), len(st2.Entries)))
			out("load", "mem", rss())
		}
		st2 = nil
		runtime.GC()
	}

	if want("registry") {
		t := time.Now()
		reg, err := st.Registry()
		out("registry", "Registry() full verify+fold", fmt.Sprintf("%v err=%v heads=%d", time.Since(t).Round(time.Millisecond), err, len(reg.Head)))
		out("registry", "mem", rss())
	}

	if want("resolve") {
		reg := &crdt.Registry{All: es}
		r := mrand.New(mrand.NewSource(1))
		probe("resolve", min(N, 500), func(int) {
			if _, _, ok := reg.Resolve(handle(r.Intn(N))); !ok {
				panic("resolve miss")
			}
		})
	}

	if want("audit") {
		t := time.Now()
		rep := checkpoint.Audit(es)
		out("audit", "checkpoint.Audit", fmt.Sprintf("%v sigOK=%d orphans=%d", time.Since(t).Round(time.Millisecond), rep.SigOK, len(rep.Orphans)))
	}

	if want("checkpoint") {
		t := time.Now()
		heads := checkpoint.ChainHeads(es)
		out("checkpoint", "ChainHeads", fmt.Sprintf("%v (%d heads)", time.Since(t).Round(time.Millisecond), len(heads)))
		t = time.Now()
		root := merkle.Root(checkpoint.Hashes(es))
		out("checkpoint", "merkle.Root", time.Since(t).Round(time.Millisecond))
		// committee: the three issuers closest to the target
		t = time.Now()
		comm := checkpoint.SelectCommittee(checkpoint.Target(root, int64(N)), checkpoint.Issuers(es))
		out("checkpoint", "SelectCommittee", time.Since(t).Round(time.Millisecond))
		privs := map[string]ed25519.PrivateKey{}
		for i := range es {
			for _, c := range comm {
				if string(es[i].PubKey) == string(c) {
					privs[string(c)] = key(i)
				}
			}
		}
		t = time.Now()
		cp, err := tlog.New(tlog.Payload{Kind: tlog.KindCheckpoint, TS: ts + 7000, Root: root, Count: int64(N),
			Comm: comm, Heads: heads}, privs[string(comm[0])], es[0].Hash)
		if err != nil {
			out("checkpoint", "ERROR", err)
		} else {
			cs, _ := tlog.CoSign(cp, privs[string(comm[1])])
			cp.CoSigs = append(cp.CoSigs, cs)
			out("checkpoint", "sign+cosign 2-of-3", time.Since(t).Round(time.Millisecond))
			t = time.Now()
			verr := cp.Verify()
			out("checkpoint", "verify", fmt.Sprintf("%v err=%v", time.Since(t).Round(time.Millisecond), verr))
			b, _ := cp.Encode()
			fr := transport.SplitFrames(cp.HashHex(), b)
			out("checkpoint", "checkpoint entry size", fmt.Sprintf("%d bytes, %d TGP1 frames", len(b), len(fr)))
			out("checkpoint", "publish @20 msg/min", (time.Duration(len(fr)) * 3 * time.Second).Round(time.Second))
			// pin verification of a bootstrap dump
			all := append(append([]tlog.Entry(nil), es...), *cp)
			t = time.Now()
			ok, reason := checkpoint.DumpVerdict(all, cp.HashHex())
			out("checkpoint", "DumpVerdict (pin check)", fmt.Sprintf("%v ok=%v %s", time.Since(t).Round(time.Millisecond), ok, reason))
		}
		out("checkpoint", "mem", rss())
	}

	if want("merge") {
		// three nodes, each missing a different third: gossip converges?
		var parts [3]*crdt.Registry
		for k := 0; k < 3; k++ {
			parts[k] = crdt.New()
		}
		for i := range es {
			for k := 0; k < 3; k++ {
				if i%3 != k {
					parts[k].Apply(es[i])
				}
			}
		}
		t := time.Now()
		m1 := crdt.Merge(parts[0], parts[1], parts[2])
		out("merge", "Merge 3 nodes", fmt.Sprintf("%v all=%d heads=%d", time.Since(t).Round(time.Millisecond), len(m1.All), len(m1.Head)))
		m2 := crdt.Merge(parts[2], parts[0], parts[1])
		same := len(m1.All) == len(m2.All) && len(m1.All) == N
		for i := 0; same && i < len(m1.All); i++ {
			same = string(m1.All[i].Hash) == string(m2.All[i].Hash)
		}
		out("merge", "order-independent & complete", same)
		out("merge", "mem", rss())
	}

	if want("gossip") {
		gossip(es)
	}
	if want("swap") {
		swaps(es, ts)
	}
	if want("bot") {
		bot(es)
	}
	if want("telegram") {
		frames := 0
		for i := 0; i < min(N, 10000); i++ {
			b, _ := es[i].Encode()
			frames += len(transport.SplitFrames(es[i].HashHex(), b))
		}
		perEntry := float64(frames) / float64(min(N, 10000))
		msgs := int(perEntry * float64(N))
		out("telegram", "messages to publish N entries", msgs)
		out("telegram", "publish time @20 msg/min/channel", (time.Duration(msgs) * 3 * time.Second).String())
		out("telegram", "bootstrap /dump replay @20 msg/min", (time.Duration(msgs) * 3 * time.Second).String())
		out("telegram", "channel bytes (base64)", fmt.Sprintf("%.1f MiB", float64(msgs)*float64(len(func() string { b, _ := es[0].Encode(); return transport.SplitFrames(es[0].HashHex(), b)[0] }()))/(1<<20)))
	}
	out("end", "mem", rss())
}

// stream signs n registers one at a time, keeping only their hashes, so
// scales too big to hold as entries still get measured: signing rate,
// wire bytes and frames, Merkle root and the hash-index memory a store
// would need at minimum.
func stream(count int, ts int64) {
	hashes := make([]string, count)
	var bytes, frames int64
	var mu sync.Mutex
	t := time.Now()
	var wg sync.WaitGroup
	w := runtime.NumCPU()
	for k := 0; k < w; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			var b, f int64
			for i := k; i < count; i += w {
				var seed [32]byte
				binary.LittleEndian.PutUint64(seed[:], uint64(i)+1)
				priv := ed25519.NewKeyFromSeed(seed[:])
				e, err := tlog.New(tlog.Payload{Kind: tlog.KindRegister, Handle: handle(i),
					DID: "did:key:z6Mk" + strconv.Itoa(i), TS: ts + int64(i%3600)}, priv, nil)
				if err != nil {
					panic(err)
				}
				enc, _ := e.Encode()
				b += int64(len(enc))
				f += int64(len(transport.SplitFrames(e.HashHex(), enc)))
				hashes[i] = e.HashHex()
			}
			mu.Lock()
			bytes += b
			frames += f
			mu.Unlock()
		}(k)
	}
	wg.Wait()
	out("stream", "keygen+sign+encode", rate(count, time.Since(t)))
	out("stream", "wire JSON total", fmt.Sprintf("%.2f GiB", float64(bytes)/(1<<30)))
	out("stream", "TGP1 frames", frames)
	out("stream", "publish @20 msg/min/channel", fmt.Sprintf("%.1f days", float64(frames)*3/86400))
	out("stream", "mem (hashes only)", rss())
	t = time.Now()
	root := merkle.Root(hashes)
	out("stream", "merkle.Root", fmt.Sprintf("%v (%s…)", time.Since(t).Round(time.Millisecond), root[:8]))
	t = time.Now()
	idx := make(map[string]int, count)
	for i, h := range hashes {
		idx[h] = i
	}
	out("stream", "hash index build", fmt.Sprintf("%v (%d keys)", time.Since(t).Round(time.Millisecond), len(idx)))
	out("stream", "mem (hashes+index)", rss())
}

// gossip publishes up to -gossip-n entries from node A to a FileBus
// (the local stand-in for a Telegram channel) and has node B ingest them
// through the real Syncer, saving data.json as a serving node does.
func gossip(es []tlog.Entry) {
	k := min(len(es), *gossipN)
	bus := filepath.Join(*dir, fmt.Sprintf("bus-%d.jsonl", k))
	os.Remove(bus)
	os.Remove(filepath.Join(*dir, "b-data.json"))
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	a := &store.Store{Tips: map[string]string{}, Seen: map[string]bool{}, Peers: map[string]*store.Peer{}}
	for i := 0; i < k; i++ {
		a.AppendEntry(&es[i])
	}
	ca, _ := transport.OpenFileBus(bus, "")
	sa := transport.NewSyncer([]transport.Conn{ca}, []string{"bus"}, a, priv)
	t := time.Now()
	sent, err := sa.PublishPending()
	out("gossip", "A publish to FileBus", fmt.Sprintf("%s err=%v", rate(sent, time.Since(t)), err))
	b, _ := store.Open(filepath.Join(*dir, "b-data.json"))
	cb, _ := transport.OpenFileBus(bus, "")
	sb := transport.NewSyncer([]transport.Conn{cb}, []string{"bus"}, b, priv)
	t = time.Now()
	for len(b.Entries) < k && time.Since(t) < *budget {
		sb.PollWindow(0)
	}
	out("gossip", "B ingest via Syncer+save", fmt.Sprintf("%s (%d/%d)", rate(len(b.Entries), time.Since(t)), len(b.Entries), k))
	out("gossip", "mem", rss())
}

// swaps runs k two-party swaps (lock/lock/claim/claim) between pairs of
// users and checks ownership flipped.
func swaps(es []tlog.Entry, ts int64) {
	N := len(es)
	k := min(N/2, 1000)
	work := append([]tlog.Entry(nil), es...)
	now := ts + 7000
	t := time.Now()
	for s := 0; s < k; s++ {
		a, b := 2*s, 2*s+1
		pa, pb := key(a), key(b)
		terms := handshake.Terms{InitGive: handle(a), InitWant: handle(b),
			InitPub: es[a].PubKey, RespPub: es[b].PubKey, Expiry: now + 3600}
		pre, h := handshake.NewSecret()
		l1, _ := handshake.BuildLock(handle(a), terms, h, terms.Expiry+handshake.ClaimWindow, now, pa, es[a].Hash)
		l2, _ := handshake.BuildLock(handle(b), terms, h, terms.Expiry, now, pb, es[b].Hash)
		c1, _ := handshake.BuildClaim(handle(b), terms.SwapID(), pre, now+1, pa, l1.Hash)
		c2, _ := handshake.BuildClaim(handle(a), terms.SwapID(), pre, now+1, pb, l2.Hash)
		work = append(work, *l1, *l2, *c1, *c2)
	}
	out("swap", "build 4 entries x swaps", rate(k, time.Since(t)))
	reg := &crdt.Registry{All: work}
	bad := 0
	lat := make([]time.Duration, 0, 200)
	for s := 0; s < min(k, 200); s++ {
		t := time.Now()
		e, st, ok := reg.Resolve(handle(2 * s))
		lat = append(lat, time.Since(t))
		if !ok || st != handshake.StateOwned || string(e.PubKey) != string(es[2*s+1].PubKey) {
			bad++
		}
	}
	out("swap", "resolve after swap", pct(lat))
	out("swap", "wrong owner after swap", fmt.Sprintf("%d of %d", bad, min(k, 200)))
}

// bot drives the custodial /register path through transport.Responder:
// the node's store already holds the CLI registrations, then users
// register through DMs. Checks the per-user cap and the rate limit.
func bot(es []tlog.Entry) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	st := &store.Store{Entries: append([]tlog.Entry(nil), es...), Tips: map[string]string{},
		Seen: map[string]bool{}, Peers: map[string]*store.Peer{}}
	for i := range st.Entries {
		st.Tips[st.Entries[i].KeyHex()] = st.Entries[i].HashHex()
		st.Seen[st.Entries[i].HashHex()] = true
	}
	r := &transport.Responder{Version: "stress", Priv: priv, Limit: 1 << 30, Window: time.Minute}
	users := *botUsers
	if users == 0 {
		users = min(len(es), 2000)
	}
	fails := 0
	lat := probe("bot", users, func(i int) {
		u := int64(1_000_000_000 + i)
		rep, ok := r.Handle(st, transport.Incoming{Chat: u, User: u, Private: true, Text: "/register b" + strconv.Itoa(i)})
		if !ok || !strings.Contains(rep.HTML, "Registered") {
			fails++
		}
	})
	_ = lat
	out("bot", "register failures", fails)
	out("bot", "store entries after", len(st.Entries))
	// resolve latency on the bot path
	probe("bot-res", 50, func(i int) {
		r.Handle(st, transport.Incoming{Chat: 7, User: 7, Private: true, Text: "/resolve " + handle(i%max(len(es), 1))})
	})
	probe("bot-stats", 20, func(int) {
		r.Handle(st, transport.Incoming{Chat: 8, User: 8, Private: true, Text: "/stats"})
	})
	// cap: one user, four handles
	u := int64(42)
	var last string
	for i := 0; i < 4; i++ {
		rep, _ := r.Handle(st, transport.Incoming{Chat: u, User: u, Private: true, Text: "/register cap" + strconv.Itoa(i)})
		last = rep.HTML
	}
	out("bot", "4th handle refused (cap 3)", strings.Contains(last, "most one account can"))
	// rate limit: default 5 per 30 s
	rl := &transport.Responder{Version: "stress", Priv: priv}
	answered, notices := 0, 0
	for i := 0; i < 20; i++ {
		rep, ok := rl.Handle(st, transport.Incoming{Chat: 99, User: 99, Private: true, Text: "/version"})
		switch {
		case ok && strings.Contains(rep.HTML, "Too many"):
			notices++
		case ok:
			answered++
		}
	}
	out("bot", "rate limit 20 msgs burst", fmt.Sprintf("answered=%d notices=%d dropped=%d", answered, notices, 20-answered-notices))
	// many distinct chats: the limiter map resets past 10000 chats
	t := time.Now()
	for i := 0; i < 20000; i++ {
		rl.Handle(st, transport.Incoming{Chat: int64(5000 + i), User: int64(5000 + i), Private: true, Text: "/version"})
	}
	out("bot", "20k distinct chats /version", rate(20000, time.Since(t)))
	// victim exhausts its limit, then 10001 other chats message the bot:
	// the limiter map is reset and the victim is answered again at once.
	rl2 := &transport.Responder{Version: "stress", Priv: priv}
	victim := func() (answered int) {
		for j := 0; j < 6; j++ {
			if rep, ok := rl2.Handle(st, transport.Incoming{Chat: 1, User: 1, Private: true, Text: "/version"}); ok && !strings.Contains(rep.HTML, "Too many") {
				answered++
			}
		}
		return answered
	}
	first := victim()
	for i := 0; i < 10001; i++ {
		rl2.Handle(st, transport.Incoming{Chat: int64(100000 + i), Private: true, Text: "/version"})
	}
	second := victim()
	out("bot", "limit bypass via 10k-chat flood", fmt.Sprintf("victim answered %d then %d within one window (limit 5 per 30 s; second > 0 means bypass)", first, second))
	out("bot", "mem", rss())
}
