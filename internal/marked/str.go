package marked

import (
	"strings"
	"unicode/utf8"
)

func runeCount(s string) int {
	return utf8.RuneCountInString(s)
}

func runeCut(s string, n int) string {
	if n <= 0 {
		return s
	}
	i := 0
	for n > 0 && i < len(s) {
		if s[i] < 0x80 {
			i++
			n--
			continue
		}
		_, sz := utf8.DecodeRuneInString(s[i:])
		i += sz
		n--
	}
	return s[i:]
}

func runePrefix(s string, n int) string {
	if n <= 0 {
		return ""
	}
	i := 0
	for n > 0 && i < len(s) {
		if s[i] < 0x80 {
			i++
			n--
			continue
		}
		_, sz := utf8.DecodeRuneInString(s[i:])
		i += sz
		n--
	}
	return s[:i]
}

// jsSlice is String.prototype.slice with rune indexes (BMP == UTF-16).
func jsSlice(s string, start, end int) string {
	n := runeCount(s)
	if start < 0 {
		start += n
	}
	if end < 0 {
		end += n
	}
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if start > n {
		start = n
	}
	if end > n {
		end = n
	}
	if end < start {
		return ""
	}
	return runePrefix(runeCut(s, start), end-start)
}

func charAt(s string, i int) string {
	if i < 0 {
		return ""
	}
	n := 0
	for _, r := range s {
		if n == i {
			return string(r)
		}
		n++
	}
	return ""
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	if s == "" {
		return 0
	}
	return r
}

func lastChar(s string) string {
	if s == "" {
		return ""
	}
	return jsSlice(s, -1, runeCount(s))
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func jsTrim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

func jsTrimStart(s string) string {
	return strings.TrimLeftFunc(s, isJSSpace)
}

func jsTrimEnd(s string) string {
	return strings.TrimRightFunc(s, isJSSpace)
}

// jsSplitLimit matches String.prototype.split(sep, limit) for a literal separator.
// Unlike strings.SplitN, a limit of 1 drops everything after the first separator.
func jsSplitLimit(s, sep string, limit int) []string {
	parts := strings.Split(s, sep)
	if limit >= 0 && len(parts) > limit {
		parts = parts[:limit]
	}
	return parts
}

func firstLine(s string) string {
	parts := jsSplitLimit(s, "\n", 1)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func joinNL(prev, next string) string {
	if strings.HasSuffix(prev, "\n") {
		return prev + next
	}
	return prev + "\n" + next
}
