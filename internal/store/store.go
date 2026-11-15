package store

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

	staged    []tlog.Entry
	stagedSet map[string]bool

	// idx maps raw entry hash to its position in Entries, covering
	// Entries[:idxN]. It is built lazily and extended incrementally, so
	// admission is O(1) instead of a scan of the whole log. Entries must
	// only grow (AppendEntry); a shorter slice triggers a rebuild.
	idx  map[string]int
	idxN int
}

// find returns the position of the entry with hash h (raw bytes), or -1.
func (s *Store) find(h []byte) int {
	if s.idx == nil || s.idxN > len(s.Entries) {
		s.idx, s.idxN = make(map[string]int, len(s.Entries)), 0
	}
	for ; s.idxN < len(s.Entries); s.idxN++ {
		s.idx[string(s.Entries[s.idxN].Hash)] = s.idxN
	}
	i, ok := s.idx[string(h)]
	if !ok || i >= len(s.Entries) || !bytes.Equal(s.Entries[i].Hash, h) {
		return -1
	}
	return i
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

// Save writes the store to Path atomically: a temporary file in the
// same directory is written, synced and renamed over Path, so a crash or
// full disk mid-write leaves the previous data.json intact. A Store with
// no Path is in-memory (tests, embedding) and Save is a no-op.
func (s *Store) Save() error {
	if s.Path == "" {
		return nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.Path), filepath.Base(s.Path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o600)
	}
	if err == nil {
		err = os.Rename(tmp, s.Path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// AppendEntry admits a verified entry. It returns true when the store
// changed: a new entry, or new valid co-signatures merged into a known
// checkpoint (co-signing does not change an entry's hash). Checkpoints
// still short of quorum are kept so committee members can co-sign them.
func (s *Store) AppendEntry(e *tlog.Entry) bool {
	h := e.HashHex()
	if s.find(e.Hash) >= 0 {
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
	raw, err := hex.DecodeString(hashHex)
	if err != nil {
		return false
	}
	i := s.find(raw)
	if i < 0 {
		return false
	}
	for _, ex := range s.Entries[i].CoSigs {
		if bytes.Equal(ex.Pub, cs.Pub) {
			return false
		}
	}
	s.Entries[i].CoSigs = append(s.Entries[i].CoSigs, cs)
	return true
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

func (s *Store) BeginStage()          { s.staged, s.stagedSet = nil, nil }
func (s *Store) Staged() []tlog.Entry { return s.staged }

func (s *Store) StageEntry(e *tlog.Entry) bool {
	if s.stagedSet == nil {
		s.stagedSet = map[string]bool{}
	}
	if s.stagedSet[string(e.Hash)] {
		return false
	}
	s.stagedSet[string(e.Hash)] = true
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
	s.staged, s.stagedSet = nil, nil
	return n
}

func (s *Store) RollbackStage() { s.staged, s.stagedSet = nil, nil }
