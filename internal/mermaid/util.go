package mermaid

import "strings"

func sat(a, b int) int {
	if a <= b {
		return 0
	}
	return a - b
}

func half(n int) int {
	if n < 0 {
		// Math.floor for negatives; sizes are non-negative in practice.
		return (n - 1) / 2
	}
	return n / 2
}

func ceilDiv2(n int) int {
	if n < 0 {
		return n / 2
	}
	return (n + 1) / 2
}

// jsIsSpace matches ECMAScript \s (without the unicode flag's extra separators
// beyond the spec set): the WhiteSpace and LineTerminator code points \s covers.
func jsIsSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\v', '\f', '\r',
		0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func jsTrim(s string) string { return strings.TrimFunc(s, jsIsSpace) }

func jsTrimRight(s string) string { return strings.TrimRightFunc(s, jsIsSpace) }

func jsTrimLeft(s string) string { return strings.TrimLeftFunc(s, jsIsSpace) }

func words(s string) []string { return strings.FieldsFunc(s, jsIsSpace) }

func firstWord(s string) string {
	w := words(s)
	if len(w) == 0 {
		return ""
	}
	return w[0]
}

func splitOnce(s, sep string) (string, string, bool) {
	i := strings.Index(s, sep)
	if i < 0 {
		return "", "", false
	}
	return s[:i], s[i+len(sep):], true
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	out := s
	return &out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func asciiUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - ('a' - 'A')
		}
	}
	return string(b)
}

func hasSpace(s string) bool {
	for _, r := range s {
		if jsIsSpace(r) {
			return true
		}
	}
	return false
}
