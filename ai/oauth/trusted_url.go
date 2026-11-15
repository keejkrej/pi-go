// Ported from packages/ai/src/auth/oauth/trusted-url.ts (pi v1.5.2).

package oauth

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/ai"
	"golang.org/x/net/idna"
)

type tuHttpClientKey struct{}

func tuWithHttpClient(ctx context.Context, client *http.Client) context.Context {
	return context.WithValue(ctx, tuHttpClientKey{}, client)
}

func tuHttpClient(ctx context.Context) *http.Client {
	if ctx != nil {
		if c, ok := ctx.Value(tuHttpClientKey{}).(*http.Client); ok && c != nil {
			return c
		}
	}
	return ai.NodeHttpProxyDefaultClient()
}

const tuUpperHex = "0123456789ABCDEF"

// TrustedHttpUrl validates and normalizes an HTTP or HTTPS URL using WHATWG URL rules.
// Non-string values, invalid URLs, and non-HTTP(S) schemes return nil.
func TrustedHttpUrl(value any) *string {
	var raw string
	switch v := value.(type) {
	case string:
		raw = v
	case *string:
		if v == nil {
			return nil
		}
		raw = *v
	default:
		return nil
	}

	normalized, ok := tuNormalizeHttpUrl(raw)
	if !ok {
		return nil
	}
	return &normalized
}

func tuTrimC0AndSpace(s string) string {
	start := 0
	for start < len(s) && s[start] <= 0x20 {
		start++
	}
	end := len(s)
	for end > start && s[end-1] <= 0x20 {
		end--
	}
	return s[start:end]
}

func tuStripTabAndNewline(s string) string {
	if !strings.ContainsAny(s, "\t\n\r") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\t', '\n', '\r':
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func tuNormalizeHttpUrl(raw string) (string, bool) {
	s := tuStripTabAndNewline(tuTrimC0AndSpace(raw))
	colon := strings.IndexByte(s, ':')
	if colon <= 0 {
		return "", false
	}
	scheme := strings.ToLower(s[:colon])
	if scheme != "http" && scheme != "https" {
		return "", false
	}
	rest := strings.TrimLeft(s[colon+1:], "/")
	if rest == "" {
		return "", false
	}

	delim := strings.IndexAny(rest, "/?#")
	var auth, tail string
	if delim < 0 {
		auth = rest
		tail = ""
	} else {
		auth = rest[:delim]
		tail = rest[delim:]
	}
	if auth == "" {
		return "", false
	}

	userinfo := ""
	hostport := auth
	if at := strings.LastIndexByte(auth, '@'); at >= 0 {
		userinfo = auth[:at]
		hostport = auth[at+1:]
		if !tuValidUserinfo(userinfo) {
			return "", false
		}
		userinfo = tuEncodeUserinfo(userinfo)
	}
	if hostport == "" {
		return "", false
	}

	normHost, normPort, ok := tuNormalizeHostPort(scheme, hostport)
	if !ok {
		return "", false
	}

	path, query, frag := tuSplitTail(tail)
	if path == "" {
		path = "/"
	}
	encPath := tuEncodePath(path)
	encQuery := ""
	if query != "" {
		encQuery = "?" + tuEncodeQuery(query[1:])
	}
	encFrag := ""
	if frag != "" {
		encFrag = "#" + tuEncodeFragment(frag[1:])
	}

	var b strings.Builder
	b.WriteString(scheme)
	b.WriteString("://")
	if userinfo != "" || strings.Contains(auth, "@") {
		b.WriteString(userinfo)
		b.WriteByte('@')
	}
	b.WriteString(normHost)
	if normPort != "" {
		b.WriteByte(':')
		b.WriteString(normPort)
	}
	b.WriteString(encPath)
	b.WriteString(encQuery)
	b.WriteString(encFrag)
	out := b.String()

	if _, err := url.Parse(out); err != nil {
		return "", false
	}
	return out, true
}

func tuValidUserinfo(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= 0x1f || s[i] == 0x7f {
			return false
		}
	}
	return true
}

func tuEncodeUserinfo(s string) string {
	// WHATWG userinfo percent-encode set encodes space, '"', '#', '<', '>', '?', '`', '{', '}', '/', ':', ';', '=', '@', '[', '\', ']', '^', '|', C0, >=0x7f.
	// When multiple colons exist, keep first unescaped ':' as username:password separator.
	if user, pass, hasPass := strings.Cut(s, ":"); hasPass {
		return tuEncodeSet(user, tuIsUserinfoForbidden) + ":" + tuEncodeSet(pass, tuIsUserinfoForbidden)
	}
	return tuEncodeSet(s, tuIsUserinfoForbidden)
}

func tuIsUserinfoForbidden(b byte) bool {
	if b <= 0x20 || b >= 0x7f {
		return true
	}
	return strings.IndexByte(`"#<>?`+"`"+`{}/:;=@[\]^|`, b) >= 0
}

func tuNormalizeHostPort(scheme, hostport string) (string, string, bool) {
	var host, port string
	if strings.HasPrefix(hostport, "[") {
		closeIdx := strings.IndexByte(hostport, ']')
		if closeIdx < 0 {
			return "", "", false
		}
		inner := hostport[1:closeIdx]
		addr, err := netip.ParseAddr(inner)
		if err != nil || !addr.Is6() {
			return "", "", false
		}
		host = "[" + addr.String() + "]"
		rem := hostport[closeIdx+1:]
		if rem != "" {
			if !strings.HasPrefix(rem, ":") {
				return "", "", false
			}
			port = rem[1:]
		}
	} else {
		colon := strings.IndexByte(hostport, ':')
		if colon >= 0 {
			host = hostport[:colon]
			port = hostport[colon+1:]
		} else {
			host = hostport
		}
		if host == "" {
			return "", "", false
		}
		for i := 0; i < len(host); i++ {
			b := host[i]
			if b <= 0x20 || b == 0x7f || strings.IndexByte("#%/:<>?@[\\]^|", b) >= 0 {
				return "", "", false
			}
		}
		asciiHost, err := idna.Lookup.ToASCII(strings.ToLower(host))
		if err != nil || asciiHost == "" {
			return "", "", false
		}
		host = asciiHost
	}

	if port != "" {
		for i := 0; i < len(port); i++ {
			if port[i] < '0' || port[i] > '9' {
				return "", "", false
			}
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 0 || n > 65535 {
			return "", "", false
		}
		port = strconv.Itoa(n)
		if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
			port = ""
		}
	}
	return host, port, true
}

func tuSplitTail(tail string) (path, query, frag string) {
	if hash := strings.IndexByte(tail, '#'); hash >= 0 {
		frag = tail[hash:]
		tail = tail[:hash]
	}
	if q := strings.IndexByte(tail, '?'); q >= 0 {
		query = tail[q:]
		path = tail[:q]
	} else {
		path = tail
	}
	return path, query, frag
}

func tuEncodePath(s string) string {
	return tuEncodeSet(s, func(b byte) bool {
		if b <= 0x20 || b >= 0x7f {
			return true
		}
		return strings.IndexByte(`"#<>?`+"`"+`{}`, b) >= 0
	})
}

func tuEncodeQuery(s string) string {
	return tuEncodeSet(s, func(b byte) bool {
		if b <= 0x20 || b >= 0x7f {
			return true
		}
		return strings.IndexByte(`"<>#`, b) >= 0
	})
}

func tuEncodeFragment(s string) string {
	return tuEncodeSet(s, func(b byte) bool {
		if b <= 0x20 || b >= 0x7f {
			return true
		}
		return strings.IndexByte(`"<>`+"`", b) >= 0
	})
}

func tuEncodeSet(s string, shouldEncode func(byte) bool) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if shouldEncode(c) {
				b.WriteByte('%')
				b.WriteByte(tuUpperHex[c>>4])
				b.WriteByte(tuUpperHex[c&15])
			} else {
				b.WriteByte(c)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		var buf [utf8.UTFMax]byte
		n := utf8.EncodeRune(buf[:], r)
		for _, octet := range buf[:n] {
			b.WriteByte('%')
			b.WriteByte(tuUpperHex[octet>>4])
			b.WriteByte(tuUpperHex[octet&15])
		}
		i += size
	}
	return b.String()
}
