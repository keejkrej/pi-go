// Ported from packages/coding-agent/src/utils/web-url.ts (pi v1.5.2).

package utils

import (
	"strings"

	"github.com/keejkrej/pi-go/internal/hostedgit"
	"github.com/keejkrej/pi-go/tui"
)

// WebUrl returns the normalized http: or https: URL that value names, or nil if value is not a web URL.
// Uses WHATWG URL normalization so control characters are percent-encoded instead of breaking terminal hyperlinks.
func WebUrl(value string) *string {
	u, err := hostedgit.NewURL(value)
	if err != nil || u == nil {
		return nil
	}
	proto := strings.ToLower(u.Protocol())
	if proto != "http:" && proto != "https:" {
		return nil
	}
	href := u.Href()
	return &href
}

// DisplayUrl returns value normalized as a web URL, or with control characters replaced so untrusted strings
// cannot terminate hyperlinks or inject terminal escape sequences.
func DisplayUrl(value string) string {
	if normalized := WebUrl(value); normalized != nil {
		return *normalized
	}
	return tui.ReplaceControlCharacters(value)
}
