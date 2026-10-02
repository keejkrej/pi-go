// Ported from node_modules/quickjs-wasi/dist/index.js (quickjs-wasi 3.6.2).

package quickjs

import (
	"strings"
	"unicode/utf8"
)

// Strings cross the wasm boundary as bytes. A Go string is already a byte
// sequence, so the TS glue's WTF-8 encoder (encodeWtf8) has no Go
// counterpart: the bytes are copied as-is. Guest strings with lone UTF-16
// surrogates come back as WTF-8 (each lone surrogate as its 3-byte sequence
// 0xED 0xA0-0xBF 0x80-0xBF), and passing such a Go string back in restores the
// original guest string.

// textDecode is new TextDecoder().decode(bytes): WHATWG UTF-8 decoding, which
// strips one leading byte order mark and replaces each maximal invalid
// subsequence with U+FFFD.
func textDecode(b []byte) string {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b) + 8)
	i := 0
	for i < len(b) {
		c := b[i]
		if c < 0x80 {
			sb.WriteByte(c)
			i++
			continue
		}
		var need int
		lower, upper := byte(0x80), byte(0xBF)
		switch {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c >= 0xE0 && c <= 0xEF:
			need = 2
			if c == 0xE0 {
				lower = 0xA0
			} else if c == 0xED {
				upper = 0x9F
			}
		case c >= 0xF0 && c <= 0xF4:
			need = 3
			if c == 0xF0 {
				lower = 0x90
			} else if c == 0xF4 {
				upper = 0x8F
			}
		default:
			sb.WriteRune(utf8.RuneError)
			i++
			continue
		}
		j := i + 1
		complete := true
		for k := range need {
			lo, hi := byte(0x80), byte(0xBF)
			if k == 0 {
				lo, hi = lower, upper
			}
			if j >= len(b) || b[j] < lo || b[j] > hi {
				complete = false
				break
			}
			j++
		}
		if complete {
			sb.Write(b[i:j])
		} else {
			// The offending byte (if any) starts the next sequence.
			sb.WriteRune(utf8.RuneError)
		}
		i = j
	}
	return sb.String()
}

// hasSurrogateSequence reports whether bytes contain a WTF-8 surrogate
// sequence (0xED followed by 0xA0-0xBF), as decodeWtf8 checks.
func hasSurrogateSequence(b []byte) bool {
	for i := 0; i < len(b)-1; i++ {
		if b[i] == 0xED && b[i+1] >= 0xA0 && b[i+1] <= 0xBF {
			return true
		}
	}
	return false
}

// decodeWtf8 decodes quickjs's WTF-8 string bytes. Strings without surrogate
// sequences take the TextDecoder path (which strips a leading byte order
// mark); strings with them are kept byte-exact, the Go form of the TS manual
// decode that preserves lone surrogates.
func decodeWtf8(b []byte) string {
	if !hasSurrogateSequence(b) {
		return textDecode(b)
	}
	return string(b)
}

// stringKeyNeedsValuePath reports whether a string-typed property key does not
// survive the NUL-terminated C-string key APIs: an embedded U+0000 truncates
// the key, and a lone surrogate (WTF-8 surrogate sequence) cannot be UTF-8
// encoded. Keys that don't survive are routed through length-aware guest
// string values instead.
func stringKeyNeedsValuePath(key string) bool {
	return strings.IndexByte(key, 0) >= 0 || hasSurrogateSequence([]byte(key))
}
