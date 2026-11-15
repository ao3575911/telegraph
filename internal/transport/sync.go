package transport

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ao3575911/telegraph/internal/checkpoint"
	"github.com/ao3575911/telegraph/internal/merkle"
	"github.com/ao3575911/telegraph/internal/store"
	"github.com/ao3575911/telegraph/internal/tlog"
)

const HBInterval = 6 * time.Hour

// CPCheckEvery is how often a serving node checks whether a checkpoint
// is due from it (as leader or fallback).
var CPCheckEvery = 10 * time.Minute

// Syncer: bots + channels + local state. Nothing arriving over Telegram
// is trusted until tlog.Entry.Verify() passes; the channel is a dumb bus.
type Syncer struct {
	Bots     []Conn
	Channels []string
	Store    *store.Store
	Priv     ed25519.PrivateKey
	asm      *Assembler
	lastCP   time.Time
	lastHB   time.Time
	staging  bool

	// Now returns the current Unix time; nil means the wall clock. Tests
	// set it to exercise fallback timing.
	Now func() int64
	// DM answers direct messages to the bot; nil ignores them.
	DM *Responder

	limiter *logLimiter
	ctx     context.Context // set by Serve; nil means never cancelled
}

func (s *Syncer) done() bool { return s.ctx != nil && s.ctx.Err() != nil }

func NewSyncer(bots []Conn, channels []string, st *store.Store,
	priv ed25519.PrivateKey) *Syncer {
	return &Syncer{Bots: bots, Channels: channels, Store: st, Priv: priv,
		asm: NewAssembler(), lastCP: time.Now(), lastHB: time.Now()}
}

func (s *Syncer) pub() []byte {
	return []byte(s.Priv.Public().(ed25519.PublicKey))
}

func nowSec() int64 { return time.Now().Unix() }

func (s *Syncer) now() int64 {
	if s.Now != nil {
		return s.Now()
	}
	return nowSec()
}

// PublishPending frames every entry not yet seen on the wire and fans it
// out through every bot x channel. Redundant on purpose. An entry is
// marked seen only when every one of its frames reached at least one
// channel; otherwise it stays pending and publishing stops for this round
// (a 429 or an outage would fail the rest too), to be retried by the next
// call. Before v0.3.0 a failed send still marked the entry seen, so it
// never reached the network.
func (s *Syncer) PublishPending() (int, error) {
	pending := s.Store.Pending()
	if len(pending) == 0 {
		return 0, nil
	}
	sent := 0
	var failed error
	for _, e := range pending {
		enc, err := e.Encode()
		if err != nil {
			return sent, err
		}
		for _, fr := range SplitFrames(e.HashHex(), enc) {
			delivered := false
			for _, bot := range s.Bots {
				for _, ch := range s.Channels {
					if err := bot.Send(ch, fr); err != nil {
						s.logf("publish %s -> %s: %v", e.HashHex()[:8], ch, err)
						failed = err
					} else {
						delivered = true
					}
				}
			}
			if !delivered {
				if err := s.Store.Save(); err != nil {
					return sent, err
				}
				return sent, fmt.Errorf("publish %s: not delivered, will retry: %w", e.HashHex()[:8], failed)
			}
		}
		s.Store.MarkSeen(e.HashHex())
		sent++
	}
	return sent, s.Store.Save()
}

// IngestText processes one text blob. Returns true when a verified
// entry was merged (or staged during bootstrap). It does not save: a
// full data.json rewrite per entry made catching up on n entries cost
// O(n^2) disk writes. PollWindow saves once per batch of updates.
func (s *Syncer) IngestText(text string) (bool, error) {
	if !IsFrame(text) {
		return false, nil
	}
	data, _, done, err := s.asm.Add(text)
	if err != nil || !done {
		return false, err
	}
	e, err := tlog.Decode(data)
	if err != nil {
		return true, fmt.Errorf("reassembled entry failed decode: %w", err)
	}
	if err := e.Verify(); err != nil && !tlog.IsPendingQuorum(err) {
		return true, fmt.Errorf("reassembled entry failed verify: %w", err)
	}
	if tlog.OldEntry(e, s.now()) && !s.Store.Seen[e.HashHex()] {
		log.Printf("note: entry %s claims ts %d (>30d old) — bootstrap replay?",
			e.HashHex()[:8], e.Payload.TS)
	}
	if s.staging {
		return s.Store.StageEntry(e), nil
	}
	if err := s.Store.CheckTakeover(e); err != nil {
		s.Store.MarkSeen(e.HashHex()) // never republish it
		return true, fmt.Errorf("entry %s for %q refused: %w", e.HashHex()[:8], clip(e.Payload.Handle, 64), err)
	}
	if merged := s.Store.AppendReceived(e); merged {
		s.MaybeCoSign()
		return true, nil // PollWindow saves once per batch
	}
	return false, nil
}

func (s *Syncer) handleUpdate(b Conn, u Update) (bool, error) {
	m := u.ChannelPost
	if m == nil {
		m = u.Message
	}
	if m == nil || m.Text == "" {
		return false, nil // edits, membership changes, media
	}
	if u.Message != nil && !s.isGossipChat(m.Chat) {
		// Direct messages and groups that are not configured gossip
		// channels: answer users, never ingest. Nothing a user types to the
		// bot can reach the gossip log, and no group can request a dump.
		s.answerUser(b, m)
		return false, nil
	}
	if !s.isGossipChat(m.Chat) {
		return false, nil // a channel this node does not gossip on
	}
	if m.Text == "/dump" {
		if s.staging {
			return false, nil // don't answer dumps while bootstrapping
		}
		s.DumpTo(b, fmt.Sprintf("%d", m.Chat.ID))
		return false, nil
	}
	return s.IngestText(m.Text)
}

// isGossipChat reports whether c is one of the configured channels (by
// numeric id or @username). The file bus has no chat ids (0) and is always
// a gossip chat.
func (s *Syncer) isGossipChat(c Chat) bool {
	if c.ID == 0 && c.Type == "" {
		return true
	}
	id := fmt.Sprintf("%d", c.ID)
	for _, ch := range s.Channels {
		if ch == id || (c.Username != "" && strings.EqualFold(strings.TrimPrefix(ch, "@"), c.Username)) {
			return true
		}
	}
	return false
}

func (s *Syncer) answerUser(b Conn, m *Msg) {
	rp, ok := b.(Replier)
	if s.DM == nil || !ok || s.staging {
		return
	}
	private := m.Chat.Type == "private"
	if !private && m.Chat.Type != "group" && m.Chat.Type != "supergroup" {
		return
	}
	in := Incoming{Chat: m.Chat.ID, Private: private, Text: m.Text, ID: m.MessageID}
	if m.From != nil && !m.From.IsBot {
		in.User = m.From.ID
	}
	reply, ok := s.safeHandle(in)
	// /register and /revoke append signed entries: gossip them now.
	if len(s.Store.Pending()) > 0 {
		if _, err := s.PublishPending(); err != nil {
			s.logf("publish: %v", err)
		}
	}
	if !ok {
		return
	}
	if err := rp.SendReply(fmt.Sprintf("%d", m.Chat.ID), reply); err != nil {
		s.logf("reply: %v", err)
	}
}

// safeHandle runs the responder, turning a panic into a logged error and
// a generic reply: one bad message must not take the node down, and no
// stack trace reaches a chat.
func (s *Syncer) safeHandle(in Incoming) (rep Reply, ok bool) {
	defer func() {
		if p := recover(); p != nil {
			rep, ok = s.DM.internalError(in, "panic", fmt.Errorf("%v", p)), true
		}
	}()
	return s.DM.Handle(s.Store, in)
}

// SetupBots fetches each bot's username (for group addressing) and
// publishes the command menu. Failures are logged, not fatal.
func (s *Syncer) SetupBots() {
	for _, c := range s.Bots {
		b, ok := c.(*Bot)
		if !ok {
			continue
		}
		if err := b.GetMe(); err != nil {
			s.logf("getMe: %v", err)
		} else if s.DM != nil && s.DM.BotUsername == "" {
			s.DM.BotUsername = b.Username
		}
		if s.DM != nil {
			if err := b.SetupMenu(s.DM.Menu()); err != nil {
				s.logf("command menu: %v", err)
			}
			short, long := s.DM.Descriptions()
			if err := b.SetupDescriptions(short, long); err != nil {
				s.logf("bot description: %v", err)
			}
		}
	}
}

func (s *Syncer) logf(format string, args ...any) {
	if s.limiter == nil {
		s.limiter = newLogLimiter(log.Printf)
	}
	s.limiter.Printf(format, args...)
}

// DumpTo streams the full local log as frames to a chat.
func (s *Syncer) DumpTo(b Conn, chat string) {
	for i := range s.Store.Entries {
		e := &s.Store.Entries[i]
		enc, err := e.Encode()
		if err != nil {
			continue
		}
		for _, fr := range SplitFrames(e.HashHex(), enc) {
			if err := b.Send(chat, fr); err != nil {
				s.logf("dump to %s: %v", chat, err)
				return
			}
		}
	}
}

func (s *Syncer) PollWindow(winSec int) (int, error) {
	merged, deadline := 0, nowSec()+int64(winSec)
	for {
		for _, b := range s.Bots {
			ups, err := b.Poll(PollTimeout)
			if err != nil {
				s.logf("poll: %v", err)
				continue
			}
			for _, u := range ups {
				if ok, err := s.handleUpdate(b, u); err != nil {
					s.logf("update: %v", err)
				} else if ok {
					merged++
				}
			}
			if len(ups) > 0 { // idle polls change nothing: skip the rewrite
				if err := s.Store.Save(); err != nil {
					s.logf("save: %v", err)
				}
			}
		}
		if nowSec() >= deadline || s.done() {
			return merged, nil
		}
	}
}

// ErrAlreadyCheckpointed: the newest checkpoint (quorate or pending)
// already covers exactly the current log, so a new one would only fork.
var ErrAlreadyCheckpointed = errors.New("current log already checkpointed")

// IssueCheckpoint builds and publishes a checkpoint over every chain this
// node holds when it leads the committee, or when it is a fallback member
// and the leader is overdue (see checkpoint.Due). It returns (nil, nil)
// when the duty belongs to other nodes.
func (s *Syncer) IssueCheckpoint() (*tlog.Entry, error) {
	cp, _, err := s.IssueCheckpointRank(false)
	return cp, err
}

// IssueCheckpointRank is IssueCheckpoint that also reports this node's
// committee rank (0 leader, 1-2 fallback, -1 not a member). With dueOnly,
// the leader too waits until the oldest uncovered entry is
// checkpoint.Interval old, as the serving loop does.
func (s *Syncer) IssueCheckpointRank(dueOnly bool) (*tlog.Entry, int, error) {
	now := s.now()
	entries := s.Store.Entries
	heads := checkpoint.ChainHeads(entries)
	log, _ := checkpoint.CoverHeads(entries, heads)
	if len(log) == 0 {
		return nil, -1, nil
	}
	root := merkle.Root(checkpoint.Hashes(log))
	for i := range entries {
		e := &entries[i]
		if e.Payload.Kind == tlog.KindCheckpoint && e.Payload.Root == root &&
			e.Payload.Count == int64(len(log)) {
			return nil, checkpoint.Rank(log, s.pub()), ErrAlreadyCheckpointed
		}
	}
	latest := s.Store.LatestCheckpoint()
	stale := checkpoint.Staleness(entries, latest, now)
	maxRank := -1
	if !dueOnly {
		maxRank = 0
	}
	for r := 2; r > maxRank; r-- {
		if checkpoint.Due(r, stale) {
			maxRank = r
			break
		}
	}
	prevHex := ""
	if latest != nil {
		prevHex = latest.HashHex()
	}
	cp, rank, err := checkpoint.BuildAt(entries, prevHex, now, maxRank, s.Priv, s.Store.Tip(s.pub()))
	if err != nil || cp == nil {
		return nil, rank, err
	}
	if !s.Store.AppendEntry(cp) {
		return nil, rank, s.Store.Save()
	}
	if _, err := s.PublishPending(); err != nil {
		return nil, rank, err
	}
	return cp, rank, s.Store.Save()
}

// MaybeCoSign: sign half-signed checkpoints we're on the committee for.
func (s *Syncer) MaybeCoSign() int {
	n := 0
	for i := range s.Store.Entries {
		e := &s.Store.Entries[i]
		if e.Payload.Kind != tlog.KindCheckpoint {
			continue
		}
		if len(e.Payload.Comm) > 1 && e.Verify() == nil {
			continue // fully quorumed already
		}
		if bytes.Equal(e.PubKey, s.pub()) {
			continue // we issued it; our signature is already the primary one
		}
		if !checkpoint.InCommittee(e.Payload.Comm, s.pub()) {
			continue
		}
		cs, err := tlog.CoSign(e, s.Priv)
		if err != nil {
			continue
		}
		if !s.Store.CoSignEntry(e.HashHex(), cs) {
			continue
		}
		s.Store.MarkUnseen(e.HashHex())
		n++
	}
	if n > 0 {
		s.Store.Save()
		s.PublishPending()
	}
	return n
}

// IssueHeartbeat: chained liveness entry carrying our pin.
func (s *Syncer) IssueHeartbeat() error {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	e, err := tlog.New(tlog.Payload{
		Kind: tlog.KindHeartbeat, Handle: "hb", DID: "alive",
		TS: s.Store.NextTS(s.pub(), nowSec()), Nonce: nonce, Pin: s.Store.Pin,
	}, s.Priv, s.Store.Tip(s.pub()))
	if err != nil {
		return err
	}
	if s.Store.AppendEntry(e) {
		if _, err := s.PublishPending(); err != nil {
			return err
		}
		return s.Store.Save()
	}
	return nil
}

// Bootstrap: request /dump everywhere, stage the window, commit iff the
// pin verdict passes. Fresh stores only.
func (s *Syncer) Bootstrap(winSec int) (int, error) {
	if len(s.Store.Entries) > 0 {
		return 0, errors.New("bootstrap only valid on an empty store")
	}
	for _, b := range s.Bots {
		for _, ch := range s.Channels {
			if err := b.Send(ch, "/dump"); err != nil {
				log.Printf("dump request -> %s: %v", ch, err)
			}
		}
	}
	s.staging = true
	s.Store.BeginStage()
	defer func() { s.staging = false }()
	merged, deadline := 0, nowSec()+int64(winSec)
	for {
		for _, b := range s.Bots {
			ups, err := b.Poll(PollTimeout)
			if err != nil {
				s.logf("poll: %v", err)
				continue
			}
			for _, u := range ups {
				if ok, err := s.handleUpdate(b, u); err != nil {
					s.logf("update: %v", err)
				} else if ok {
					merged++
				}
			}
		}
		if nowSec() >= deadline {
			break
		}
	}
	staged := s.Store.Staged()
	if len(staged) == 0 {
		return 0, errors.New("no dump received — no serving peers reachable")
	}
	ok, reason := checkpoint.DumpVerdict(staged, s.Store.Pin)
	if !ok {
		s.Store.RollbackStage()
		return 0, fmt.Errorf("dump rejected: %s", reason)
	}
	n := s.Store.CommitStage()
	s.Store.Save()
	return n, nil
}

// Serve publishes, then polls, checkpoints, heartbeats and self-heals
// until ctx is cancelled (SIGTERM/SIGINT). On the way out it saves the
// store, so a restart loses nothing it had merged.
func (s *Syncer) Serve(ctx context.Context) error {
	for _, b := range s.Bots {
		if c, ok := b.(interface{ SetContext(context.Context) }); ok {
			c.SetContext(ctx)
		}
	}
	s.ctx = ctx
	if _, err := s.PublishPending(); err != nil {
		log.Printf("publish: %v", err)
	}
	for ctx.Err() == nil {
		if len(s.Store.Pending()) > 0 { // retry entries a failed send left behind
			if _, err := s.PublishPending(); err != nil {
				log.Printf("publish: %v", err)
			}
		}
		if n, err := s.PollWindow(25); err != nil {
			log.Printf("poll window: %v", err)
		} else if n > 0 {
			log.Printf("merged %d entries", n)
		}
		if time.Since(s.lastHB) >= HBInterval {
			if err := s.IssueHeartbeat(); err != nil {
				log.Printf("heartbeat: %v", err)
			}
			s.lastHB = time.Now()
		}
		if time.Since(s.lastCP) >= CPCheckEvery {
			if cp, rank, err := s.IssueCheckpointRank(true); err != nil {
				if !errors.Is(err, ErrAlreadyCheckpointed) {
					log.Printf("checkpoint: %v", err)
				}
			} else if cp != nil && rank > 0 {
				log.Printf("issued checkpoint %s as fallback (rank %d): leader overdue", cp.HashHex()[:8], rank)
			} else if cp != nil {
				log.Printf("issued checkpoint %s (committee leader)", cp.HashHex()[:8])
			} else {
				s.MaybeCoSign()
			}
			s.lastCP = time.Now()
		}
		if stats := checkpoint.GatherStats(s.Store, nowSec()); stats.Health == "DRIFTED" {
			if newPin, reason := checkpoint.RotatePin(s.Store.Entries, s.Store.Pin); reason == "" {
				s.Store.SetPin(newPin)
				s.Store.Save()
				log.Printf("pin self-healed to %s (majority drift)", newPin[:8])
			} else {
				log.Printf("pin drift observed but rotate refused: %s", reason)
			}
		}
	}
	log.Printf("shutting down: saving store")
	return s.Store.Save()
}
