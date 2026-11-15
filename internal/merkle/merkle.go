package merkle

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// Root over hex entry hashes. Sorted first: the root is a pure function
// of set membership, so any two nodes holding the same set — in any
// arrival order — derive the identical root.
func Root(hashHexes []string) string {
	if len(hashHexes) == 0 {
		sum := sha256.Sum256(nil)
		return hex.EncodeToString(sum[:])
	}
	sorted := append([]string(nil), hashHexes...)
	sort.Strings(sorted)
	level := make([][]byte, len(sorted))
	for i, h := range sorted {
		b, _ := hex.DecodeString(h)
		level[i] = b
	}
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1])
		}
		next := make([][]byte, 0, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			var buf []byte
			buf = append(buf, level[i]...)
			buf = append(buf, level[i+1]...)
			sum := sha256.Sum256(buf)
			next = append(next, sum[:])
		}
		level = next
	}
	return hex.EncodeToString(level[0])
}
