package tlog

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

var Genesis [32]byte

type Kind uint8

const (
	KindRegister   Kind = 1
	KindRevoke     Kind = 2
	KindCheckpoint Kind = 3
	KindHeartbeat  Kind = 4
	KindOffer      Kind = 5
	KindLock       Kind = 6
	KindClaim      Kind = 7
	KindReclaim    Kind = 8
	KindCard       Kind = 9
)

const (
	MaxFutureDrift = int64(24 * 3600)
	RequiredCPSigs = 2
)

var (
	ErrBadSig       = errors.New("tlog: bad signature")
	ErrBadHash      = errors.New("tlog: bad or missing hash")
	ErrBadKey       = errors.New("tlog: bad public key")
	ErrFutureTS     = errors.New("tlog: entry timestamp more than 24h in the future")
	ErrNotCommittee = errors.New("tlog: signer not on checkpoint committee")
	ErrCPSigs       = errors.New("tlog: insufficient valid committee signatures")
)

type Cosig struct {
	Pub []byte `json:"p"`
	Sig []byte `json:"s"`
}

type Payload struct {
	Kind   Kind   `json:"k"`
	Handle string `json:"h"`
	DID    string `json:"d"`
	TS     int64  `json:"t"`
	Nonce  []byte `json:"n,omitempty"`

	Root   string   `json:"mr,omitempty"` // merkle root hex (checkpoints)
	Count  int64    `json:"mc,omitempty"` // covered entry count
	Comm   [][]byte `json:"cm,omitempty"` // committee pubkeys
	PrevCP string   `json:"pc,omitempty"` // previous checkpoint hash hex
	Heads  []string `json:"hd,omitempty"` // covered chain heads, hex (checkpoints, v0.2.0+)

	Pin    string `json:"pt,omitempty"` // issuer's trusted root (heartbeats)
	SwapID string `json:"sid,omitempty"`
	Peer   []byte `json:"sp,omitempty"`
	Hash   string `json:"sh,omitempty"` // hashlock H(S) hex
	Expiry int64  `json:"sx,omitempty"`
	Pre    []byte `json:"si,omitempty"` // S revealed on claim
}

type Entry struct {
	Payload  Payload `json:"p"`
	PubKey   []byte  `json:"pk"`
	PrevHash []byte  `json:"ph"`
	Sig      []byte  `json:"s"`
	CoSigs   []Cosig `json:"x,omitempty"`
	Hash     []byte  `json:"ha"`
}

func (e *Entry) SigningBytes() ([]byte, error) {
	clone := *e
	clone.Sig = nil
	clone.Hash = nil
	clone.CoSigs = nil
	return json.Marshal(&clone)
}

func (e *Entry) ComputeHash() []byte {
	b, _ := e.SigningBytes()
	h := sha256.Sum256(b)
	return h[:]
}

func New(p Payload, priv ed25519.PrivateKey, prevHash []byte) (*Entry, error) {
	if len(prevHash) == 0 {
		prevHash = Genesis[:]
	}
	e := &Entry{Payload: p, PubKey: []byte(priv.Public().(ed25519.PublicKey)), PrevHash: prevHash}
	b, err := e.SigningBytes()
	if err != nil {
		return nil, err
	}
	e.Sig = ed25519.Sign(priv, b)
	e.Hash = e.ComputeHash()
	return e, nil
}

func (e *Entry) Verify() error { return VerifyAt(e, time.Now().Unix()) }

// VerifyAt: hash + signature + wall-clock sanity. Past timestamps are
// NEVER rejected — old signed history stays valid forever.
//
// Successful verifications are memoised (see verifiedCache): a node
// re-verifies its whole log on every /resolve, audit and checkpoint, and
// the ed25519 check dominated those paths. The memo key covers every
// byte the verdict depends on, and a pass never turns into a failure as
// the clock advances, so a hit is exactly as strong as re-verifying.
func VerifyAt(e *Entry, now int64) error {
	if e.Payload.TS-now > MaxFutureDrift {
		return ErrFutureTS
	}
	k, ok := e.verifyKey()
	if ok && verified.has(k) {
		return nil
	}
	err := e.verifyRaw()
	if err == nil && ok {
		verified.add(k)
	}
	return err
}

// verifyKey hashes the signing bytes, claimed hash, signature and
// co-signatures: everything verifyRaw reads.
func (e *Entry) verifyKey() ([32]byte, bool) {
	b, err := e.SigningBytes()
	if err != nil {
		return [32]byte{}, false
	}
	h := sha256.New()
	h.Write(b)
	h.Write([]byte{0})
	h.Write(e.Hash)
	h.Write([]byte{0})
	h.Write(e.Sig)
	for _, c := range e.CoSigs {
		h.Write([]byte{1, byte(len(c.Pub))})
		h.Write(c.Pub)
		h.Write([]byte{byte(len(c.Sig))})
		h.Write(c.Sig)
	}
	var k [32]byte
	h.Sum(k[:0])
	return k, true
}

// verifiedCache is a bounded set of verification keys. When full it is
// cleared, which costs only re-verification.
type verifiedCache struct {
	mu sync.RWMutex
	m  map[[32]byte]struct{}
}

const maxVerified = 1 << 21

var verified = &verifiedCache{m: map[[32]byte]struct{}{}}

func (c *verifiedCache) has(k [32]byte) bool {
	c.mu.RLock()
	_, ok := c.m[k]
	c.mu.RUnlock()
	return ok
}

func (c *verifiedCache) add(k [32]byte) {
	c.mu.Lock()
	if len(c.m) >= maxVerified {
		c.m = map[[32]byte]struct{}{}
	}
	c.m[k] = struct{}{}
	c.mu.Unlock()
}

func (e *Entry) verifyRaw() error {
	if len(e.PubKey) != ed25519.PublicKeySize {
		return ErrBadKey
	}
	if !bytes.Equal(e.ComputeHash(), e.Hash) {
		return ErrBadHash
	}
	b, err := e.SigningBytes()
	if err != nil {
		return err
	}
	if len(e.Sig) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(e.PubKey), b, e.Sig) {
		return ErrBadSig
	}
	if e.Payload.Kind == KindCheckpoint {
		return verifyCheckpoint(e, b)
	}
	if len(e.CoSigs) > 0 { // only checkpoints carry co-sigs
		return ErrBadSig
	}
	return nil
}

func verifyCheckpoint(e *Entry, preimage []byte) error {
	member := map[string]bool{}
	for _, c := range e.Payload.Comm {
		member[string(c)] = true
	}
	if len(member) > 0 && !member[string(e.PubKey)] {
		return ErrNotCommittee
	}
	if CommitteeSet() {
		if len(member) == 0 {
			return ErrOffCommittee
		}
		for _, c := range e.Payload.Comm {
			if !CommitteeAllows(c) {
				return ErrOffCommittee
			}
		}
	}
	counted := map[string]bool{string(e.PubKey): true}
	for _, cs := range e.CoSigs {
		if len(member) > 0 && !member[string(cs.Pub)] {
			continue
		}
		if len(cs.Sig) != ed25519.SignatureSize {
			continue
		}
		if !ed25519.Verify(ed25519.PublicKey(cs.Pub), preimage, cs.Sig) {
			continue
		}
		counted[string(cs.Pub)] = true
	}
	if len(member) > 1 && len(counted) < RequiredCPSigs {
		return ErrCPSigs
	}
	return nil
}

func CoSign(e *Entry, priv ed25519.PrivateKey) (Cosig, error) {
	b, err := e.SigningBytes()
	if err != nil {
		return Cosig{}, err
	}
	return Cosig{
		Pub: []byte(priv.Public().(ed25519.PublicKey)),
		Sig: ed25519.Sign(priv, b),
	}, nil
}

func (e *Entry) Encode() ([]byte, error) { return json.Marshal(e) }

func Decode(b []byte) (*Entry, error) {
	e := &Entry{}
	if err := json.Unmarshal(b, e); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Entry) HashHex() string { return hex.EncodeToString(e.Hash) }
func (e *Entry) KeyHex() string  { return hex.EncodeToString(e.PubKey) }
func (e *Entry) PrevHex() string { return hex.EncodeToString(e.PrevHash) }

func OldEntry(e *Entry, now int64) bool { return now-e.Payload.TS > 30*24*int64(3600) }

// IsPendingQuorum reports whether err only says a checkpoint lacks enough
// committee co-signatures yet. Such a checkpoint is well-formed and
// signed by a committee member; stores keep it so others can co-sign.
func IsPendingQuorum(err error) bool { return errors.Is(err, ErrCPSigs) }

// ValidCosig reports whether cs is a committee member's valid co-signature
// over checkpoint e.
func ValidCosig(e *Entry, cs Cosig) bool {
	if e.Payload.Kind != KindCheckpoint || len(cs.Pub) != ed25519.PublicKeySize ||
		len(cs.Sig) != ed25519.SignatureSize {
		return false
	}
	member := len(e.Payload.Comm) == 0
	for _, c := range e.Payload.Comm {
		if bytes.Equal(c, cs.Pub) {
			member = true
		}
	}
	b, err := e.SigningBytes()
	return member && err == nil && ed25519.Verify(ed25519.PublicKey(cs.Pub), b, cs.Sig)
}
