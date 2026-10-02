package xspawn

import "strings"

// metaChars is cross-spawn's metaCharsRegExp, including space.
// Backslash is not a meta character.
const metaChars = "()[]%!^\"`<>&|;, *?"

func isMeta(c byte) bool {
	return strings.IndexByte(metaChars, c) >= 0
}

// escapeCommand is cross-spawn escape.command.
func escapeCommand(arg string) string {
	return escapeMeta(arg)
}

// escapeArgument is cross-spawn escape.argument.
// doubleEscapeMetaChars is set for cmd shims under node_modules/.bin.
func escapeArgument(arg string, doubleEscapeMetaChars bool) string {
	arg = escapeCmdQuotes(arg)
	arg = `"` + arg + `"`
	arg = escapeMeta(arg)
	if doubleEscapeMetaChars {
		arg = escapeMeta(arg)
	}
	return arg
}

// escapeCmdQuotes applies cross-spawn's quote regular expressions.
// /(?=(\\+?)?)\1"/ and the same pattern at end-of-string match one
// backslash, not the whole run: a quote becomes \", a backslash before a
// quote or at the end is doubled once, and any further backslashes stay.
func escapeCmdQuotes(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		if s[i] != '\\' && s[i] != '"' {
			b.WriteByte(s[i])
			i++
			continue
		}
		if s[i] == '"' {
			b.WriteString(`\"`)
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '\\' {
			j++
		}
		n := j - i
		if j < len(s) && s[j] == '"' {
			if n > 1 {
				b.WriteString(strings.Repeat(`\`, n-1))
			}
			b.WriteString(`\\\"`)
			i = j + 1
			continue
		}
		if j == len(s) {
			if n > 1 {
				b.WriteString(strings.Repeat(`\`, n-1))
			}
			b.WriteString(`\\`)
			return b.String()
		}
		b.WriteString(s[i:j])
		i = j
	}
	return b.String()
}

func escapeMeta(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		if isMeta(s[i]) {
			b.WriteByte('^')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
