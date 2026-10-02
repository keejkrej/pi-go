// Ported from the WHATWG URL Standard host parsing section (host, IPv4, IPv6, opaque host, domain to ASCII),
// as implemented by Node's `URL` (ada 4.0.0).

package hostedgit

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// hostParse is the host parser; it returns the serialized host.
func hostParse(input string, isOpaque bool) (string, bool) {
	if strings.HasPrefix(input, "[") {
		if !strings.HasSuffix(input, "]") || len(input) < 2 {
			return "", false
		}
		address, ok := ipv6Parse([]rune(input[1 : len(input)-1]))
		if !ok {
			return "", false
		}
		return "[" + ipv6Serialize(address) + "]", true
	}
	if isOpaque {
		return opaqueHostParse(input)
	}
	domain := percentDecode(input)
	asciiDomain, ok := domainToASCII(domain)
	if !ok {
		return "", false
	}
	if endsInANumber(asciiDomain) {
		address, ok := ipv4Parse(asciiDomain)
		if !ok {
			return "", false
		}
		return ipv4Serialize(address), true
	}
	return asciiDomain, true
}

func isForbiddenHostCodePoint(c rune) bool {
	switch c {
	case 0, '\t', '\n', '\r', ' ', '#', '/', ':', '<', '>', '?', '@', '[', '\\', ']', '^', '|':
		return true
	}
	return false
}

func isForbiddenDomainCodePoint(c rune) bool {
	return isForbiddenHostCodePoint(c) || (c >= 0 && c <= 0x1f) || c == '%' || c == 0x7f
}

func opaqueHostParse(input string) (string, bool) {
	for _, c := range input {
		if isForbiddenHostCodePoint(c) {
			return "", false
		}
	}
	return utf8PercentEncodeString(input, inC0ControlSet), true
}

func fromHexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// percentDecode percent-decodes the UTF-8 bytes of input; invalid escapes are kept as is.
func percentDecode(input string) string {
	if !strings.Contains(input, "%") {
		return input
	}
	out := make([]byte, 0, len(input))
	for i := 0; i < len(input); i++ {
		c := input[i]
		if c == '%' && i+2 < len(input) {
			hi, ok1 := fromHexDigit(input[i+1])
			lo, ok2 := fromHexDigit(input[i+2])
			if ok1 && ok2 {
				out = append(out, hi<<4|lo)
				i += 2
				continue
			}
		}
		out = append(out, c)
	}
	return string(out)
}

var idnaProfile = idna.New(
	idna.MapForLookup(),
	idna.BidiRule(),
	idna.Transitional(false),
	idna.StrictDomainName(false),
	idna.CheckHyphens(false),
	idna.CheckJoiners(true),
	idna.VerifyDNSLength(false),
)

// domainToASCII is "domain to ASCII" with beStrict false. Like Node (ada), an ASCII domain is only
// lowercased: its xn-- labels are not checked for valid Punycode, so "xn--abc" is accepted.
func domainToASCII(domain string) (string, bool) {
	result := ""
	if isASCIIString(domain) {
		result = strings.ToLower(domain)
	} else {
		// The bytes come from percent-decoding, so they may not be UTF-8; the UTF-8 decoder would turn
		// such bytes into U+FFFD, which UTS #46 disallows.
		if !utf8.ValidString(domain) {
			return "", false
		}
		if hasEmptyPunycodeLabel(domain) {
			return "", false
		}
		ascii, err := idnaProfile.ToASCII(domain)
		if err != nil {
			return "", false
		}
		result = ascii
	}
	if result == "" {
		return "", false
	}
	for _, c := range result {
		if isForbiddenDomainCodePoint(c) {
			return "", false
		}
	}
	return result, true
}

// idnaMapProfile maps single code points the way idnaProfile does, without the label checks that a lone
// code point could fail.
var idnaMapProfile = idna.New(
	idna.MapForLookup(),
	idna.Transitional(false),
	idna.StrictDomainName(false),
	idna.CheckHyphens(false),
	idna.CheckJoiners(false),
	idna.VerifyDNSLength(false),
)

// hasEmptyPunycodeLabel reports whether a label of domain maps (UTS #46) to exactly "xn--". ada rejects
// such a label because its Punycode part is empty, while x/net/idna turns it into an empty label.
func hasEmptyPunycodeLabel(domain string) bool {
	var mapped strings.Builder
	for _, r := range domain {
		if r < utf8.RuneSelf {
			mapped.WriteRune(asciiLower(r))
			continue
		}
		m, err := idnaMapProfile.ToUnicode(string(r))
		if err != nil {
			// A code point that does not map cleanly cannot be part of an "xn--" label.
			mapped.WriteRune(r)
			continue
		}
		mapped.WriteString(m)
	}
	for label := range strings.SplitSeq(mapped.String(), ".") {
		if label == "xn--" {
			return true
		}
	}
	return false
}

func isASCIIString(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// endsInANumber is the "ends in a number" checker.
func endsInANumber(input string) bool {
	parts := strings.Split(input, ".")
	if parts[len(parts)-1] == "" {
		if len(parts) == 1 {
			return false
		}
		parts = parts[:len(parts)-1]
	}
	last := parts[len(parts)-1]
	if last != "" && strings.Trim(last, "0123456789") == "" {
		return true
	}
	_, ok := ipv4NumberParse(last)
	return ok
}

// ipv4NumberSaturation caps parsed numbers; anything at or above 2^32 fails the IPv4 parser anyway.
const ipv4NumberSaturation = uint64(1) << 40

// ipv4NumberParse is the IPv4 number parser (the validation-error flag is not needed).
func ipv4NumberParse(input string) (uint64, bool) {
	if input == "" {
		return 0, false
	}
	radix := uint64(10)
	if len(input) >= 2 && (input[:2] == "0x" || input[:2] == "0X") {
		input = input[2:]
		radix = 16
	} else if len(input) >= 2 && input[0] == '0' {
		input = input[1:]
		radix = 8
	}
	if input == "" {
		return 0, true
	}
	var n uint64
	for i := 0; i < len(input); i++ {
		c := input[i]
		var d uint64
		switch {
		case c >= '0' && c <= '9':
			d = uint64(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint64(c-'A') + 10
		default:
			return 0, false
		}
		if d >= radix {
			return 0, false
		}
		if n < ipv4NumberSaturation {
			n = n*radix + d
		}
	}
	return n, true
}

// ipv4Parse is the IPv4 parser.
func ipv4Parse(input string) (uint32, bool) {
	parts := strings.Split(input, ".")
	if parts[len(parts)-1] == "" && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > 4 {
		return 0, false
	}
	numbers := make([]uint64, 0, len(parts))
	for _, part := range parts {
		n, ok := ipv4NumberParse(part)
		if !ok {
			return 0, false
		}
		numbers = append(numbers, n)
	}
	for _, n := range numbers[:len(numbers)-1] {
		if n > 255 {
			return 0, false
		}
	}
	last := numbers[len(numbers)-1]
	if last >= uint64(1)<<(8*(5-len(numbers))) {
		return 0, false
	}
	ipv4 := last
	for i, n := range numbers[:len(numbers)-1] {
		ipv4 += n << (8 * (3 - i))
	}
	return uint32(ipv4), true
}

func ipv4Serialize(address uint32) string {
	return strconv.Itoa(int(address>>24)) + "." + strconv.Itoa(int(address>>16&255)) + "." +
		strconv.Itoa(int(address>>8&255)) + "." + strconv.Itoa(int(address&255))
}

func hexDigitValue(c rune) int {
	v, _ := fromHexDigit(byte(c))
	return int(v)
}

// ipv6Parse is the IPv6 parser.
func ipv6Parse(input []rune) ([8]uint16, bool) {
	var address [8]uint16
	pieceIndex := 0
	compress := -1
	pointer := 0
	at := func(i int) rune {
		if i < 0 || i >= len(input) {
			return eof
		}
		return input[i]
	}

	if at(pointer) == ':' {
		if at(pointer+1) != ':' {
			return address, false
		}
		pointer += 2
		pieceIndex++
		compress = pieceIndex
	}

	for at(pointer) != eof {
		if pieceIndex == 8 {
			return address, false
		}
		if at(pointer) == ':' {
			if compress != -1 {
				return address, false
			}
			pointer++
			pieceIndex++
			compress = pieceIndex
			continue
		}
		value, length := 0, 0
		for length < 4 && isASCIIHexDigit(at(pointer)) {
			value = value*0x10 + hexDigitValue(at(pointer))
			pointer++
			length++
		}
		if at(pointer) == '.' {
			if length == 0 {
				return address, false
			}
			pointer -= length
			if pieceIndex > 6 {
				return address, false
			}
			numbersSeen := 0
			for at(pointer) != eof {
				ipv4Piece := -1
				if numbersSeen > 0 {
					if at(pointer) == '.' && numbersSeen < 4 {
						pointer++
					} else {
						return address, false
					}
				}
				if !isASCIIDigit(at(pointer)) {
					return address, false
				}
				for isASCIIDigit(at(pointer)) {
					number := int(at(pointer) - '0')
					if ipv4Piece == -1 {
						ipv4Piece = number
					} else if ipv4Piece == 0 {
						return address, false
					} else {
						ipv4Piece = ipv4Piece*10 + number
					}
					if ipv4Piece > 255 {
						return address, false
					}
					pointer++
				}
				address[pieceIndex] = address[pieceIndex]*0x100 + uint16(ipv4Piece)
				numbersSeen++
				if numbersSeen == 2 || numbersSeen == 4 {
					pieceIndex++
				}
			}
			if numbersSeen != 4 {
				return address, false
			}
			break
		} else if at(pointer) == ':' {
			pointer++
			if at(pointer) == eof {
				return address, false
			}
		} else if at(pointer) != eof {
			return address, false
		}
		address[pieceIndex] = uint16(value)
		pieceIndex++
	}

	if compress != -1 {
		swaps := pieceIndex - compress
		pieceIndex = 7
		for pieceIndex != 0 && swaps > 0 {
			address[pieceIndex], address[compress+swaps-1] = address[compress+swaps-1], address[pieceIndex]
			pieceIndex--
			swaps--
		}
	} else if pieceIndex != 8 {
		return address, false
	}
	return address, true
}

// ipv6Serialize is the IPv6 serializer (the longest run of two or more zero pieces is compressed).
func ipv6Serialize(address [8]uint16) string {
	compress, bestLen := -1, 1
	for i := 0; i < 8; {
		if address[i] != 0 {
			i++
			continue
		}
		j := i
		for j < 8 && address[j] == 0 {
			j++
		}
		if j-i > bestLen {
			compress, bestLen = i, j-i
		}
		i = j
	}
	var b strings.Builder
	ignore0 := false
	for pieceIndex := 0; pieceIndex < 8; pieceIndex++ {
		if ignore0 && address[pieceIndex] == 0 {
			continue
		}
		ignore0 = false
		if compress == pieceIndex {
			if pieceIndex == 0 {
				b.WriteString("::")
			} else {
				b.WriteString(":")
			}
			ignore0 = true
			continue
		}
		b.WriteString(strconv.FormatUint(uint64(address[pieceIndex]), 16))
		if pieceIndex != 7 {
			b.WriteString(":")
		}
	}
	return b.String()
}
