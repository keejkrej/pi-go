// Ported from packages/ai/src/utils/hash.ts (pi v1.5.2).

package ai

import (
	"strconv"

	"github.com/keejkrej/pi-go/internal/js"
)

// ShortHash computes a fast deterministic hash to shorten long strings,
// matching TS `shortHash(str)` using UTF-16 code units and 32-bit `Math.imul`.
func ShortHash(str string) string {
	var h1 uint32 = 0xdeadbeef
	var h2 uint32 = 0x41c6ce57
	for _, ch := range js.ToUTF16(str) {
		c := uint32(ch)
		h1 = (h1 ^ c) * 2654435761
		h2 = (h2 ^ c) * 1597334677
	}
	h1 = ((h1 ^ (h1 >> 16)) * 2246822507) ^ ((h2 ^ (h2 >> 13)) * 3266489909)
	h2 = ((h2 ^ (h2 >> 16)) * 2246822507) ^ ((h1 ^ (h1 >> 13)) * 3266489909)
	return strconv.FormatUint(uint64(h2), 36) + strconv.FormatUint(uint64(h1), 36)
}
