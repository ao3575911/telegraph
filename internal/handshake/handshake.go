package handshake

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"

	"github.com/ao3575911/telegraph/internal/tlog"
)

var (
	ErrNoLock      = errors.New("handshake: no matching lock")
	ErrBadPreimage = errors.New("handshake: preimage does not match hashlock")
	ErrNotPeer     = errors.New("handshake: claimant not authorised by lock")
	ErrExpired     = errors.New("handshake: claim past expiry")
	ErrTooEarly    = errors.New("handshake: reclaim before expiry")
	ErrBadOwner    = errors.New("handshake: wrong owner for reclaim")
	ErrBadStagger  = errors.New("handshake: receive-lock must expire FIRST (stagger violated)")
)

// ClaimWindow (Δ): the initiator's give-lock must outlive the receive-
// lock by a full window, else late-claim races let one side take both.
const ClaimWindow = int64(48 * 3600)

type Terms struct {
	InitGive string `json:"ig"`
	InitWant string `json:"iw"`
	InitPub  []byte `json:"ip"`
	RespPub  []byte `json:"rp"`
	Expiry   int64  `json:"ex"` // receive-lock expiry; give-lock = Expiry+ClaimWindow
}

func (t Terms) Canonical() []byte {
	return []byte(hex.EncodeToString(t.InitPub) + "|" +
		hex.EncodeToString(t.RespPub) + "|" +
		t.InitGive + "|" + t.InitWant + "|" + strconv.FormatInt(t.Expiry, 10))
}

func (t Terms) SwapID() string {
	sum := sha256.Sum256(t.Canonical())
	return hex.EncodeToString(sum[:])
}

func NewSecret() (pre []byte, hash string) {
	pre = make([]byte, 32)
	rand.Read(pre)
	sum := sha256.Sum256(pre)
	return pre, hex.EncodeToString(sum[:])
}

// ValidateStagger: clients MUST call this on the counterparty's lock
// before accepting. Equal expiries create a late-claim theft race.
func ValidateStagger(give, take *tlog.Entry) error {
	if give.Payload.SwapID != take.Payload.SwapID {
		return ErrNoLock
	}
	if take.Payload.Expiry+ClaimWindow > give.Payload.Expiry {
		return ErrBadStagger
	}
	return nil
}

func ValidClaim(lock, claim *tlog.Entry) error {
	if lock == nil || claim.Payload.SwapID != lock.Payload.SwapID ||
		claim.Payload.Handle != lock.Payload.Handle {
		return ErrNoLock
	}
	if !bytes.Equal(claim.PubKey, lock.Payload.Peer) {
		return ErrNotPeer
	}
	sum := sha256.Sum256(claim.Payload.Pre)
	if hex.EncodeToString(sum[:]) != lock.Payload.Hash {
		return ErrBadPreimage
	}
	if claim.Payload.TS > lock.Payload.Expiry {
		return ErrExpired
	}
	return nil
}

func ValidReclaim(lock, reclaim *tlog.Entry) error {
	if reclaim.Payload.SwapID != lock.Payload.SwapID {
		return ErrNoLock
	}
	if !bytes.Equal(reclaim.PubKey, lock.PubKey) {
		return ErrBadOwner
	}
	if reclaim.Payload.TS <= lock.Payload.Expiry {
		return ErrTooEarly
	}
	return nil
}

// BuildLock creates one lock leg. Lock the give-side with
// Expiry = terms.Expiry + ClaimWindow; the take-side with Expiry.
func BuildLock(handle string, t Terms, hash string, expiry int64,
	ts int64, priv ed25519.PrivateKey, tip []byte) (*tlog.Entry, error) {
	var peer []byte
	if handle == t.InitGive {
		peer = t.RespPub
	} else {
		peer = t.InitPub
	}
	nonce := make([]byte, 8)
	rand.Read(nonce)
	return tlog.New(tlog.Payload{
		Kind: tlog.KindLock, Handle: handle, SwapID: t.SwapID(),
		Peer: peer, Hash: hash, Expiry: expiry, TS: ts, Nonce: nonce,
	}, priv, tip)
}

// BuildClaim publishes the preimage, taking a locked handle.
func BuildClaim(handle, sid string, pre []byte, ts int64,
	priv ed25519.PrivateKey, tip []byte) (*tlog.Entry, error) {
	nonce := make([]byte, 8)
	rand.Read(nonce)
	return tlog.New(tlog.Payload{
		Kind: tlog.KindClaim, Handle: handle, SwapID: sid,
		Pre: pre, TS: ts, Nonce: nonce,
	}, priv, tip)
}

func BuildReclaim(handle, sid string, ts int64,
	priv ed25519.PrivateKey, tip []byte) (*tlog.Entry, error) {
	nonce := make([]byte, 8)
	rand.Read(nonce)
	return tlog.New(tlog.Payload{
		Kind: tlog.KindReclaim, Handle: handle, SwapID: sid,
		TS: ts, Nonce: nonce,
	}, priv, tip)
}
