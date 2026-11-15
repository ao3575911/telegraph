package transport

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
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
}

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
// out through every bot x channel. Redundant on purpose.
func (s *Syncer) PublishPending() (int, error) {
	pending := s.Store.Pending()
	if len(pending) == 0 {
		return 0, nil
	}
	sent := 0
	for _, e := range pending {
		enc, err := e.Encode()
		if err != nil {
			return sent, err
		}
		for _, fr := range SplitFrames(e.HashHex(), enc) {
			for _, bot := range s.Bots {
				for _, ch := range s.Channels {
					if err := bot.Send(ch, fr); err != nil {
						log.Printf("publish %s -> %s: %v", e.HashHex()[:8], ch, err)
					}
				}
			}
		}
		s.Store.MarkSeen(e.HashHex())
		sent++
	}
	return sent, s.Store.Save()
}

// IngestText processes one text blob. Returns true when a verified
// entry was merged (or staged during bootstrap).
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
	if merged := s.Store.AppendReceived(e); merged {
		s.MaybeCoSign()
		return true, s.Store.Save()
	}
	return false, nil
}

func (s *Syncer) handleUpdate(b Conn, u Update) (bool, error) {
	if u.Message != nil && u.Message.Chat.Type == "private" {
		// Direct messages are answered, never ingested: nothing a user
		// types to the bot can reach the gossip log.
		s.answerDM(b, u.Message)
		return false, nil
	}
	text, replyChat := "", ""
	if u.ChannelPost != nil {
		text = u.ChannelPost.Text
		replyChat = fmt.Sprintf("%d", u.ChannelPost.Chat.ID)
	} else if u.Message != nil {
		text, replyChat = u.Message.Text, fmt.Sprintf("%d", u.Message.Chat.ID)
	}
	if text == "" {
		return false, nil
	}
	if text == "/dump" {
		if s.staging {
			return false, nil // don't answer dumps while bootstrapping
		}
		s.DumpTo(b, replyChat)
		return false, nil
	}
	return s.IngestText(text)
}

func (s *Syncer) answerDM(b Conn, m *Msg) {
	hs, ok := b.(HTMLSender)
	if s.DM == nil || !ok {
		return
	}
	reply, ok := s.DM.Handle(s.Store, m.Chat.ID, m.Text)
	if !ok {
		return
	}
	if err := hs.SendHTML(fmt.Sprintf("%d", m.Chat.ID), reply); err != nil {
		log.Printf("dm reply: %v", err)
	}
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
				log.Printf("dump to %s: %v", chat, err)
				return
			}
		}
	}
}

func (s *Syncer) PollWindow(winSec int) (int, error) {
	merged, deadline := 0, nowSec()+int64(winSec)
	for {
		for _, b := range s.Bots {
			ups, err := b.Poll(25)
			if err != nil {
				log.Printf("poll: %v", err)
				continue
			}
			for _, u := range ups {
				if ok, err := s.handleUpdate(b, u); err != nil {
					log.Printf("update: %v", err)
				} else if ok {
					merged++
				}
			}
		}
		s.Store.Save()
		if nowSec() >= deadline {
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
		TS: nowSec(), Nonce: nonce, Pin: s.Store.Pin,
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
			ups, err := b.Poll(25)
			if err != nil {
				log.Printf("poll: %v", err)
				continue
			}
			for _, u := range ups {
				if ok, err := s.handleUpdate(b, u); err != nil {
					log.Printf("update: %v", err)
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

// Serve: publish, then poll + checkpoint + heartbeat + self-heal forever.
func (s *Syncer) Serve() {
	if _, err := s.PublishPending(); err != nil {
		log.Printf("publish: %v", err)
	}
	for {
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
}
