package transport

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

const Protocol = "TGP1"
const MaxChunk = 2800

var ErrBadFrame = errors.New("transport: malformed frame")

// Frame: TGP1|<id>|<idx>|<total>|<base64-chunk>. id = entry hash hex,
// so reassembly is self-verifying.
func SplitFrames(id string, data []byte) []string {
	total := (len(data) + MaxChunk - 1) / MaxChunk
	if total == 0 {
		total = 1
	}
	frames := make([]string, total)
	for i := 0; i < total; i++ {
		lo := i * MaxChunk
		hi := lo + MaxChunk
		if hi > len(data) {
			hi = len(data)
		}
		frames[i] = fmt.Sprintf("%s|%s|%d|%d|%s", Protocol, id, i, total,
			base64.StdEncoding.EncodeToString(data[lo:hi]))
	}
	return frames
}

func IsFrame(s string) bool { return strings.HasPrefix(s, Protocol+"|") }

type Assembler struct {
	mu    sync.Mutex
	parts map[string][][]byte
	have  map[string]int
}

func NewAssembler() *Assembler {
	return &Assembler{parts: map[string][][]byte{}, have: map[string]int{}}
}

func (a *Assembler) Add(frame string) (data []byte, id string, ok bool, err error) {
	f := strings.SplitN(frame, "|", 5)
	if len(f) != 5 || f[0] != Protocol {
		return nil, "", false, ErrBadFrame
	}
	id = f[1]
	idx, err1 := strconv.Atoi(f[2])
	total, err2 := strconv.Atoi(f[3])
	if err1 != nil || err2 != nil || idx < 0 || total < 1 || idx >= total {
		return nil, "", false, ErrBadFrame
	}
	chunk, err3 := base64.StdEncoding.DecodeString(f[4])
	if err3 != nil {
		return nil, "", false, ErrBadFrame
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.parts[id] == nil {
		a.parts[id] = make([][]byte, total)
	}
	if a.parts[id][idx] == nil {
		a.parts[id][idx] = chunk
		a.have[id]++
	}
	if a.have[id] == len(a.parts[id]) {
		out := []byte{}
		for _, c := range a.parts[id] {
			out = append(out, c...)
		}
		delete(a.parts, id)
		delete(a.have, id)
		return out, id, true, nil
	}
	return nil, id, false, nil
}
