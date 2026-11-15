package store

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ao3575911/telegraph/internal/crdt"
	"github.com/ao3575911/telegraph/internal/tlog"
)

type Peer struct {
	LastSeen int64  `json:"last"`
	LastHB   string `json:"hb"`
}

type Store struct {
	Path    string            `json:"-"`
	Entries []tlog.Entry      `json:"entries"`
	Tips    map[string]string `json:"tips"`
	Seen    map[string]bool   `json:"seen"`

	Pin      string           `json:"pin"`
	PinSetAt int64            `json:"pinAt"`
	Peers    map[string]*Peer `json:"peers"`

	// Custody maps handles this node signed for Telegram users (bot
	// /register) to the user id allowed to change or revoke them. It is
	// node-local and never gossiped.
	Custody map[string]int64 `json:"custody,omitempty"`

	staged []tlog.Entry
}

func Open(path string) (*Store, error) {
	s := &Store{Path: path, Tips: map[string]string{},
		Seen: map[string]bool{}, Peers: map[string]*Peer{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("corrupt store %s: %w", path, err)
	}
	if s.Tips == nil {
		s.Tips = map[string]string{}
	}
	if s.Seen == nil {
		s.Seen = map[string]bool{}
	}
	if s.Peers == nil {
		s.Peers = map[string]*Peer{}
	}
	return s, nil
}

// Save writes the store to Path. A Store with no Path is in-memory
// (tests, embedding) and Save is a no-op.
func (s *Store) Save() error {
	if s.Path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.Path, b, 0o600)
}

// AppendEntry admits a verified entry. It returns true when the store
// changed: a new entry, or new valid co-signatures merged into a known
// checkpoint (co-signing does not change an entry's hash). Checkpoints
// still short of quorum are kept so committee members can co-sign them.
func (s *Store) AppendEntry(e *tlog.Entry) bool {
	h := e.HashHex()
	if s.has(h) {
		return s.mergeCosigs(e)
	}
	if err := e.Verify(); err != nil && !tlog.IsPendingQuorum(err) {
		return false // never admit an unverifiable entry
	}
	s.Entries = append(s.Entries, *e)
	s.Tips[e.KeyHex()] = h
	s.RecordHeartbeat(e)
	return true
}

// AppendReceived admits an entry that arrived from the wire and marks it
// seen there, so it is not republished. Locally created entries go
// through AppendEntry and stay pending until PublishPending sends them.
func (s *Store) AppendReceived(e *tlog.Entry) bool {
	ok := s.AppendEntry(e)
	s.MarkSeen(e.HashHex())
	return ok
}

func (s *Store) has(hashHex string) bool {
	for i := range s.Entries {
		if s.Entries[i].HashHex() == hashHex {
			return true
		}
	}
	return false
}

func (s *Store) mergeCosigs(e *tlog.Entry) bool {
	if e.Payload.Kind != tlog.KindCheckpoint {
		return false
	}
	added := false
	for _, cs := range e.CoSigs {
		if tlog.ValidCosig(e, cs) && s.CoSignEntry(e.HashHex(), cs) {
			added = true
		}
	}
	return added
}

func (s *Store) RecordHeartbeat(e *tlog.Entry) {
	if e.Payload.Kind != tlog.KindHeartbeat {
		return
	}
	k := e.KeyHex()
	p, ok := s.Peers[k]
	if !ok || e.Payload.TS > p.LastSeen {
		s.Peers[k] = &Peer{LastSeen: e.Payload.TS, LastHB: e.HashHex()}
	}
}

func (s *Store) Tip(pub []byte) []byte {
	h, ok := s.Tips[hex.EncodeToString(pub)]
	if !ok {
		return tlog.Genesis[:]
	}
	b, _ := hex.DecodeString(h)
	return b
}

func (s *Store) MarkSeen(hashHex string) {
	if hashHex != "" {
		s.Seen[hashHex] = true
	}
}

func (s *Store) MarkUnseen(hashHex string) { delete(s.Seen, hashHex) }

func (s *Store) Pending() []*tlog.Entry {
	var out []*tlog.Entry
	for i := range s.Entries {
		if !s.Seen[s.Entries[i].HashHex()] {
			out = append(out, &s.Entries[i])
		}
	}
	return out
}

// LogEntries: checkpoint-eligible set — excludes checkpoints and
// heartbeats, else nodes with different heartbeat subsets fork.
func (s *Store) LogEntries() []tlog.Entry {
	var out []tlog.Entry
	for i := range s.Entries {
		k := s.Entries[i].Payload.Kind
		if k != tlog.KindCheckpoint && k != tlog.KindHeartbeat {
			out = append(out, s.Entries[i])
		}
	}
	return out
}

func (s *Store) Registry() (*crdt.Registry, error) {
	return crdt.FromEntries(s.Entries)
}

func (s *Store) CoSignEntry(hashHex string, cs tlog.Cosig) bool {
	for i := range s.Entries {
		if s.Entries[i].HashHex() != hashHex {
			continue
		}
		for _, ex := range s.Entries[i].CoSigs {
			if bytes.Equal(ex.Pub, cs.Pub) {
				return false
			}
		}
		s.Entries[i].CoSigs = append(s.Entries[i].CoSigs, cs)
		return true
	}
	return false
}

func (s *Store) LatestCheckpoint() *tlog.Entry {
	var best *tlog.Entry
	for i := range s.Entries {
		e := &s.Entries[i]
		if e.Payload.Kind != tlog.KindCheckpoint || e.Verify() != nil {
			continue
		}
		if best == nil || e.Payload.TS > best.Payload.TS ||
			(e.Payload.TS == best.Payload.TS && bytes.Compare(e.Hash, best.Hash) > 0) {
			best = e
		}
	}
	return best
}

func (s *Store) SetPin(hashHex string) {
	s.Pin = hashHex
	s.PinSetAt = time.Now().Unix()
}

// ---- staging: transactional bootstrap ----

func (s *Store) BeginStage()          { s.staged = nil }
func (s *Store) Staged() []tlog.Entry { return s.staged }

func (s *Store) StageEntry(e *tlog.Entry) bool {
	h := e.HashHex()
	for i := range s.staged {
		if s.staged[i].HashHex() == h {
			return false
		}
	}
	s.staged = append(s.staged, *e)
	return true
}

func (s *Store) CommitStage() int {
	n := 0
	for i := range s.staged {
		if s.AppendReceived(&s.staged[i]) {
			n++
		}
	}
	s.staged = nil
	return n
}

func (s *Store) RollbackStage() { s.staged = nil }
