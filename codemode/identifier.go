// Ported from packages/codemode/src/identifier.ts (pi v1.0.0).

package codemode

import "strings"

func idenIsFirstChar(ch rune) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || ch == '_' || ch == '$'
}

func idenIsNextChar(ch rune) bool {
	return idenIsFirstChar(ch) || (ch >= '0' && ch <= '9')
}

// ToCodemodeIdentifier is the identifier a script uses for a tool: characters that
// are not valid in a JavaScript identifier become `_`. `mcp__docs__search` stays
// as is, `my-tool` becomes `my_tool`. An empty name becomes `_`.
func ToCodemodeIdentifier(name string) string {
	if name == "" {
		return "_"
	}
	var out strings.Builder
	out.Grow(len(name))
	first := true
	for _, ch := range name {
		var ok bool
		if first {
			ok = idenIsFirstChar(ch)
			first = false
		} else {
			ok = idenIsNextChar(ch)
		}
		if ok {
			out.WriteRune(ch)
		} else {
			out.WriteByte('_')
		}
	}
	return out.String()
}
