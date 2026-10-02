package js

import (
	"strings"
	"unicode/utf8"
)

const upperHex = "0123456789ABCDEF"

// uriUnreserved reports whether ASCII byte c is in uriAlpha, DecimalDigit, or
// uriMark ("-_.!~*'()").
func uriUnreserved(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.IndexByte("-_.!~*'()", c) >= 0
}

// uriReserved is the uriReserved set plus "#", which encodeURI and decodeURI
// leave alone.
const uriReservedPlusHash = ";/?:@&=+$,#"

func encodeURI(s string, extra string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if uriUnreserved(c) || (extra != "" && strings.IndexByte(extra, c) >= 0) {
				b.WriteByte(c)
			} else {
				b.WriteByte('%')
				b.WriteByte(upperHex[c>>4])
				b.WriteByte(upperHex[c&15])
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		var buf [utf8.UTFMax]byte
		n := utf8.EncodeRune(buf[:], r)
		for _, octet := range buf[:n] {
			b.WriteByte('%')
			b.WriteByte(upperHex[octet>>4])
			b.WriteByte(upperHex[octet&15])
		}
		i += size
	}
	return b.String()
}

// EncodeURIComponent returns encodeURIComponent(s). Invalid UTF-8 bytes encode
// as U+FFFD (%EF%BF%BD).
func EncodeURIComponent(s string) string {
	return encodeURI(s, "")
}

// EncodeURI returns encodeURI(s), which also leaves ";/?:@&=+$,#" unescaped.
// Invalid UTF-8 bytes encode as U+FFFD (%EF%BF%BD).
func EncodeURI(s string) string {
	return encodeURI(s, uriReservedPlusHash)
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func decodeHexOctet(s string, k int) (byte, bool) {
	if k+2 >= len(s) {
		return 0, false
	}
	h, l := hexVal(s[k+1]), hexVal(s[k+2])
	if h < 0 || l < 0 {
		return 0, false
	}
	return byte(h<<4 | l), true
}

func uriMalformed() error {
	return NewURIError("URI malformed")
}

// decodeURI implements the ES Decode(string, preserveEscapeSet) operation.
func decodeURI(s string, preserve string) (string, error) {
	if strings.IndexByte(s, '%') < 0 {
		return s, nil
	}
	var b strings.Builder
	for k := 0; k < len(s); k++ {
		c := s[k]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		start := k
		if k+2 >= len(s) {
			return "", uriMalformed()
		}
		octet, ok := decodeHexOctet(s, k)
		if !ok {
			return "", uriMalformed()
		}
		k += 2
		if octet < 0x80 {
			if preserve != "" && strings.IndexByte(preserve, octet) >= 0 {
				b.WriteString(s[start : k+1])
			} else {
				b.WriteByte(octet)
			}
			continue
		}
		n := 0
		for mask := byte(0x80); octet&mask != 0; mask >>= 1 {
			n++
		}
		if n == 1 || n > 4 {
			return "", uriMalformed()
		}
		octets := []byte{octet}
		if k+3*(n-1) >= len(s) {
			return "", uriMalformed()
		}
		for j := 1; j < n; j++ {
			k++
			if s[k] != '%' {
				return "", uriMalformed()
			}
			next, ok := decodeHexOctet(s, k)
			if !ok || next&0xC0 != 0x80 {
				return "", uriMalformed()
			}
			k += 2
			octets = append(octets, next)
		}
		if !utf8.Valid(octets) {
			return "", uriMalformed()
		}
		b.Write(octets)
	}
	return b.String(), nil
}

// DecodeURIComponent returns decodeURIComponent(s). Malformed escapes return a
// URIError "URI malformed".
func DecodeURIComponent(s string) (string, error) {
	return decodeURI(s, "")
}

// DecodeURI returns decodeURI(s), which keeps escapes of ";/?:@&=+$,#" as is.
// Malformed escapes return a URIError "URI malformed".
func DecodeURI(s string) (string, error) {
	return decodeURI(s, uriReservedPlusHash)
}
