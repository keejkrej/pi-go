// Ported from the ECMAScript built-ins hosted-git-info relies on: encodeURIComponent, decodeURIComponent,
// String.prototype.toLowerCase and the RegExp `\s` and `\W` classes.

package hostedgit

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// URIError is the error decodeURIComponent throws for malformed percent-encoding.
type URIError struct{}

func (e *URIError) Error() string { return "URI malformed" }

// Name is the JS error name.
func (e *URIError) Name() string { return "URIError" }

// TypeError is a JS TypeError surfaced by FromUrlChecked.
type TypeError struct {
	Message string
}

func (e *TypeError) Error() string { return e.Message }

// Name is the JS error name.
func (e *TypeError) Name() string { return "TypeError" }

// isJSWhitespace reports whether c matches the RegExp `\s` class (WhiteSpace and LineTerminator).
func isJSWhitespace(c rune) bool {
	switch c {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return c >= 0x2000 && c <= 0x200a
}

// isJSWordChar reports whether c matches the non-unicode RegExp `\w` class.
func isJSWordChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

func isURIUnreserved(c byte) bool {
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
		return true
	}
	switch c {
	case '-', '_', '.', '!', '~', '*', '\'', '(', ')':
		return true
	}
	return false
}

// encodeURIComponent is the JS global of the same name. Invalid UTF-8 bytes (which a JS string cannot hold)
// are encoded as U+FFFD.
func encodeURIComponent(s string) string {
	var b strings.Builder
	var buf [4]byte
	for _, r := range s {
		if r < utf8.RuneSelf && isURIUnreserved(byte(r)) {
			b.WriteByte(byte(r))
			continue
		}
		n := utf8.EncodeRune(buf[:], r)
		for i := 0; i < n; i++ {
			b.WriteByte('%')
			b.WriteByte(upperHex[buf[i]>>4])
			b.WriteByte(upperHex[buf[i]&15])
		}
	}
	return b.String()
}

func decodeHexByte(s string, i int) (byte, bool) {
	if i+2 >= len(s) || s[i] != '%' {
		return 0, false
	}
	hi, ok1 := fromHexDigit(s[i+1])
	lo, ok2 := fromHexDigit(s[i+2])
	if !ok1 || !ok2 {
		return 0, false
	}
	return hi<<4 | lo, true
}

// jsString is template-literal interpolation of a string-or-null value.
func jsString(s *string) string {
	if s == nil {
		return "null"
	}
	return *s
}

// orString is `s || fallback` for string-or-null values.
func orString(s *string, fallback string) string {
	if s == nil || *s == "" {
		return fallback
	}
	return *s
}

// decodeURIComponent is the JS global of the same name; malformed input yields a *URIError.
func decodeURIComponent(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b strings.Builder
	for k := 0; k < len(s); k++ {
		if s[k] != '%' {
			b.WriteByte(s[k])
			continue
		}
		first, ok := decodeHexByte(s, k)
		if !ok {
			return "", &URIError{}
		}
		k += 2
		if first < 0x80 {
			b.WriteByte(first)
			continue
		}
		n := 0
		for first<<n&0x80 != 0 {
			n++
		}
		if n == 1 || n > 4 {
			return "", &URIError{}
		}
		octets := []byte{first}
		if k+3*(n-1) >= len(s) {
			return "", &URIError{}
		}
		for j := 1; j < n; j++ {
			k++
			octet, ok := decodeHexByte(s, k)
			if !ok || octet&0xc0 != 0x80 {
				return "", &URIError{}
			}
			k += 2
			octets = append(octets, octet)
		}
		if r, size := utf8.DecodeRune(octets); r == utf8.RuneError || size != n {
			return "", &URIError{}
		}
		b.Write(octets)
	}
	return b.String(), nil
}

var jsLower = cases.Lower(language.Und)

// formatHashFragment is hosts.js formatHashFragment.
func formatHashFragment(f string) string {
	f = jsLower.String(f)
	// strip leading non-characters
	f = strings.TrimLeftFunc(f, func(r rune) bool { return !isJSWordChar(r) })
	// strip trailing non-characters
	f = strings.TrimRightFunc(f, func(r rune) bool { return !isJSWordChar(r) })
	// strip all slashes
	f = strings.ReplaceAll(f, "/", "")
	// replace remaining non-characters with '-'
	var b strings.Builder
	inRun := false
	for _, r := range f {
		if isJSWordChar(r) {
			b.WriteRune(r)
			inRun = false
		} else if !inRun {
			b.WriteByte('-')
			inRun = true
		}
	}
	return b.String()
}
