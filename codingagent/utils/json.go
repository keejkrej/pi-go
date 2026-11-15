// Ported from packages/coding-agent/src/utils/json.ts (pi v1.5.2).

package utils

import (
	"strings"

	"github.com/keejkrej/pi-go/internal/jsre"
)

var (
	jsonCommentRe       = jsre.MustCompile(`"(?:\\.|[^"\\])*"|//[^\n]*`, "g")
	jsonTrailingCommaRe = jsre.MustCompile(`"(?:\\.|[^"\\])*"|,(\s*[}\]])`, "g")
)

// StripJsonComments strips `//` line comments and trailing commas from JSON, leaving string literals untouched.
func StripJsonComments(input string) string {
	withoutComments := jsonCommentRe.ReplaceFunc(input, func(m *jsre.Match) string {
		s := m.String()
		if strings.HasPrefix(s, `"`) {
			return s
		}
		return ""
	})
	return jsonTrailingCommaRe.ReplaceFunc(withoutComments, func(m *jsre.Match) string {
		if tail, ok := m.GroupOK(1); ok {
			return tail
		}
		s := m.String()
		if strings.HasPrefix(s, `"`) {
			return s
		}
		return ""
	})
}
