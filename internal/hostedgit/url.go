// Ported from the WHATWG URL Standard basic URL parser, as implemented by Node's `URL` (ada 4.0.0).
// hosted-git-info parses every git URL with `new URL()`, so its results depend on these exact rules.

package hostedgit

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// URL is a WHATWG URL record with the getters and setters of the JS `URL` class.
type URL struct {
	scheme   string
	username string
	password string
	host     *string // serialized host; nil is the null host
	port     int     // -1 is the null port
	// path is the path segment list, or the single opaque path when opaque is true.
	path     []string
	opaque   bool
	query    *string
	fragment *string
}

// InvalidURLError is the TypeError `new URL()` throws (code ERR_INVALID_URL).
type InvalidURLError struct {
	Input string
	Base  *string
}

func (e *InvalidURLError) Error() string { return "Invalid URL" }

// Name is the JS error name.
func (e *InvalidURLError) Name() string { return "TypeError" }

// Code is the Node error code.
func (e *InvalidURLError) Code() string { return "ERR_INVALID_URL" }

// NewURL is `new URL(input)`.
func NewURL(input string) (*URL, error) {
	u, ok := basicURLParse(input, nil, nil, stateNone)
	if !ok {
		return nil, &InvalidURLError{Input: input}
	}
	return u, nil
}

// NewURLWithBase is `new URL(input, base)`.
func NewURLWithBase(input, base string) (*URL, error) {
	b, ok := basicURLParse(base, nil, nil, stateNone)
	if !ok {
		return nil, &InvalidURLError{Input: input, Base: &base}
	}
	u, ok := basicURLParse(input, b, nil, stateNone)
	if !ok {
		return nil, &InvalidURLError{Input: input, Base: &base}
	}
	return u, nil
}

// URLCanParse is the static `URL.canParse(input)`.
func URLCanParse(input string) bool {
	_, ok := basicURLParse(input, nil, nil, stateNone)
	return ok
}

type urlState int

const (
	stateNone urlState = iota
	stateSchemeStart
	stateScheme
	stateNoScheme
	stateSpecialRelativeOrAuthority
	statePathOrAuthority
	stateRelative
	stateRelativeSlash
	stateSpecialAuthoritySlashes
	stateSpecialAuthorityIgnoreSlashes
	stateAuthority
	stateHost
	stateHostname
	statePort
	stateFile
	stateFileSlash
	stateFileHost
	statePathStart
	statePath
	stateOpaquePath
	stateQuery
	stateFragment
)

var specialSchemes = map[string]int{
	"ftp":   21,
	"file":  -1,
	"http":  80,
	"https": 443,
	"ws":    80,
	"wss":   443,
}

func isSpecialScheme(scheme string) bool {
	_, ok := specialSchemes[scheme]
	return ok
}

func defaultPort(scheme string) int {
	if p, ok := specialSchemes[scheme]; ok {
		return p
	}
	return -1
}

func (u *URL) isSpecial() bool { return isSpecialScheme(u.scheme) }

func (u *URL) includesCredentials() bool { return u.username != "" || u.password != "" }

func (u *URL) cannotHaveUsernamePasswordPort() bool {
	return u.host == nil || *u.host == "" || u.scheme == "file"
}

func strPtr(s string) *string { return &s }

const eof = rune(-1)

func isASCIIAlpha(c rune) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isASCIIDigit(c rune) bool { return c >= '0' && c <= '9' }

func isASCIIHexDigit(c rune) bool {
	return isASCIIDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func isASCIIAlphanumeric(c rune) bool { return isASCIIAlpha(c) || isASCIIDigit(c) }

func asciiLower(c rune) rune {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// Percent-encode sets.
func inC0ControlSet(c rune) bool { return c < 0x20 || c > 0x7e }

func inFragmentSet(c rune) bool {
	return inC0ControlSet(c) || c == ' ' || c == '"' || c == '<' || c == '>' || c == '`'
}

func inQuerySet(c rune) bool {
	return inC0ControlSet(c) || c == ' ' || c == '"' || c == '#' || c == '<' || c == '>'
}

func inSpecialQuerySet(c rune) bool { return inQuerySet(c) || c == '\'' }

func inPathSet(c rune) bool {
	return inQuerySet(c) || c == '?' || c == '^' || c == '`' || c == '{' || c == '}'
}

func inUserinfoSet(c rune) bool {
	return inPathSet(c) || c == '/' || c == ':' || c == ';' || c == '=' || c == '@' ||
		(c >= '[' && c <= '^') || c == '|'
}

const upperHex = "0123456789ABCDEF"

// utf8PercentEncodeRune appends c, percent-encoding its UTF-8 bytes when c is in the set.
func utf8PercentEncodeRune(b *strings.Builder, c rune, inSet func(rune) bool) {
	if !inSet(c) {
		b.WriteRune(c)
		return
	}
	var buf [4]byte
	n := utf8.EncodeRune(buf[:], c)
	for i := 0; i < n; i++ {
		b.WriteByte('%')
		b.WriteByte(upperHex[buf[i]>>4])
		b.WriteByte(upperHex[buf[i]&15])
	}
}

func utf8PercentEncodeString(s string, inSet func(rune) bool) string {
	var b strings.Builder
	for _, c := range s {
		utf8PercentEncodeRune(&b, c, inSet)
	}
	return b.String()
}

func isWindowsDriveLetter(s []rune) bool {
	return len(s) == 2 && isASCIIAlpha(s[0]) && (s[1] == ':' || s[1] == '|')
}

func isNormalizedWindowsDriveLetter(s string) bool {
	return len(s) == 2 && isASCIIAlpha(rune(s[0])) && s[1] == ':'
}

func startsWithWindowsDriveLetter(s []rune) bool {
	if len(s) < 2 || !isWindowsDriveLetter(s[:2]) {
		return false
	}
	if len(s) == 2 {
		return true
	}
	switch s[2] {
	case '/', '\\', '?', '#':
		return true
	}
	return false
}

func isSingleDotSegment(s string) bool {
	return s == "." || strings.EqualFold(s, "%2e")
}

func isDoubleDotSegment(s string) bool {
	switch strings.ToLower(s) {
	case "..", ".%2e", "%2e.", "%2e%2e":
		return true
	}
	return false
}

func (u *URL) shortenPath() {
	if u.scheme == "file" && len(u.path) == 1 && isNormalizedWindowsDriveLetter(u.path[0]) {
		return
	}
	if len(u.path) > 0 {
		u.path = u.path[:len(u.path)-1]
	}
}

func isC0ControlOrSpace(c rune) bool { return c >= 0 && c <= 0x20 }

// basicURLParse is the basic URL parser. url is non-nil with a state override (setters). ok is false on
// failure; with a state override, a "return" without failure also reports ok.
func basicURLParse(input string, base *URL, url *URL, stateOverride urlState) (*URL, bool) {
	if url == nil {
		url = &URL{port: -1}
		// Remove any leading and trailing C0 control or space.
		input = strings.TrimFunc(input, isC0ControlOrSpace)
	}
	// Remove all ASCII tab or newline.
	if strings.ContainsAny(input, "\t\n\r") {
		input = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(input)
	}

	runes := []rune(input)
	state := stateOverride
	if state == stateNone {
		state = stateSchemeStart
	}

	var buffer []rune
	atSignSeen, insideBrackets, passwordTokenSeen := false, false, false
	pointer := 0

	at := func(i int) rune {
		if i < 0 || i >= len(runes) {
			return eof
		}
		return runes[i]
	}
	// remainingStartsWith reports whether the code points after pointer start with s.
	remainingStartsWith := func(s string) bool {
		i := pointer + 1
		for _, r := range s {
			if at(i) != r {
				return false
			}
			i++
		}
		return true
	}

	for {
		c := at(pointer)
		switch state {
		case stateSchemeStart:
			if isASCIIAlpha(c) {
				buffer = append(buffer, asciiLower(c))
				state = stateScheme
			} else if stateOverride == stateNone {
				state = stateNoScheme
				pointer--
			} else {
				return url, false
			}

		case stateScheme:
			if isASCIIAlphanumeric(c) || c == '+' || c == '-' || c == '.' {
				buffer = append(buffer, asciiLower(c))
			} else if c == ':' {
				bufStr := string(buffer)
				if stateOverride != stateNone {
					if url.isSpecial() != isSpecialScheme(bufStr) {
						return url, true
					}
					if (url.includesCredentials() || url.port != -1) && bufStr == "file" {
						return url, true
					}
					if url.scheme == "file" && url.host != nil && *url.host == "" {
						return url, true
					}
				}
				url.scheme = bufStr
				if stateOverride != stateNone {
					if url.port == defaultPort(url.scheme) {
						url.port = -1
					}
					return url, true
				}
				buffer = buffer[:0]
				if url.scheme == "file" {
					state = stateFile
				} else if url.isSpecial() && base != nil && base.scheme == url.scheme {
					state = stateSpecialRelativeOrAuthority
				} else if url.isSpecial() {
					state = stateSpecialAuthoritySlashes
				} else if remainingStartsWith("/") {
					state = statePathOrAuthority
					pointer++
				} else {
					url.opaque = true
					url.path = []string{""}
					state = stateOpaquePath
				}
			} else if stateOverride == stateNone {
				buffer = buffer[:0]
				state = stateNoScheme
				pointer = -1
			} else {
				return url, false
			}

		case stateNoScheme:
			if base == nil || (base.opaque && c != '#') {
				return url, false
			} else if base.opaque && c == '#' {
				url.scheme = base.scheme
				url.path = append([]string(nil), base.path...)
				url.opaque = true
				url.query = base.query
				url.fragment = strPtr("")
				state = stateFragment
			} else if base.scheme != "file" {
				state = stateRelative
				pointer--
			} else {
				state = stateFile
				pointer--
			}

		case stateSpecialRelativeOrAuthority:
			if c == '/' && remainingStartsWith("/") {
				state = stateSpecialAuthorityIgnoreSlashes
				pointer++
			} else {
				state = stateRelative
				pointer--
			}

		case statePathOrAuthority:
			if c == '/' {
				state = stateAuthority
			} else {
				state = statePath
				pointer--
			}

		case stateRelative:
			url.scheme = base.scheme
			if c == '/' {
				state = stateRelativeSlash
			} else if url.isSpecial() && c == '\\' {
				state = stateRelativeSlash
			} else {
				url.username = base.username
				url.password = base.password
				url.host = base.host
				url.port = base.port
				url.path = append([]string(nil), base.path...)
				url.query = base.query
				if c == '?' {
					url.query = strPtr("")
					state = stateQuery
				} else if c == '#' {
					url.fragment = strPtr("")
					state = stateFragment
				} else if c != eof {
					url.query = nil
					url.shortenPath()
					state = statePath
					pointer--
				}
			}

		case stateRelativeSlash:
			if url.isSpecial() && (c == '/' || c == '\\') {
				state = stateSpecialAuthorityIgnoreSlashes
			} else if c == '/' {
				state = stateAuthority
			} else {
				url.username = base.username
				url.password = base.password
				url.host = base.host
				url.port = base.port
				state = statePath
				pointer--
			}

		case stateSpecialAuthoritySlashes:
			if c == '/' && remainingStartsWith("/") {
				state = stateSpecialAuthorityIgnoreSlashes
				pointer++
			} else {
				state = stateSpecialAuthorityIgnoreSlashes
				pointer--
			}

		case stateSpecialAuthorityIgnoreSlashes:
			if c != '/' && c != '\\' {
				state = stateAuthority
				pointer--
			}

		case stateAuthority:
			if c == '@' {
				if atSignSeen {
					buffer = append([]rune("%40"), buffer...)
				}
				atSignSeen = true
				var user, pass strings.Builder
				user.WriteString(url.username)
				pass.WriteString(url.password)
				for _, cp := range buffer {
					if cp == ':' && !passwordTokenSeen {
						passwordTokenSeen = true
						continue
					}
					if passwordTokenSeen {
						utf8PercentEncodeRune(&pass, cp, inUserinfoSet)
					} else {
						utf8PercentEncodeRune(&user, cp, inUserinfoSet)
					}
				}
				url.username = user.String()
				url.password = pass.String()
				buffer = buffer[:0]
			} else if c == eof || c == '/' || c == '?' || c == '#' || (url.isSpecial() && c == '\\') {
				if atSignSeen && len(buffer) == 0 {
					return url, false
				}
				pointer -= len(buffer) + 1
				buffer = buffer[:0]
				state = stateHost
			} else {
				buffer = append(buffer, c)
			}

		case stateHost, stateHostname:
			if stateOverride != stateNone && url.scheme == "file" {
				pointer--
				state = stateFileHost
			} else if c == ':' && !insideBrackets {
				if len(buffer) == 0 {
					return url, false
				}
				if stateOverride == stateHostname {
					return url, false
				}
				host, ok := hostParse(string(buffer), !url.isSpecial())
				if !ok {
					return url, false
				}
				url.host = &host
				buffer = buffer[:0]
				state = statePort
			} else if c == eof || c == '/' || c == '?' || c == '#' || (url.isSpecial() && c == '\\') {
				pointer--
				if url.isSpecial() && len(buffer) == 0 {
					return url, false
				} else if stateOverride != stateNone && len(buffer) == 0 && (url.includesCredentials() || url.port != -1) {
					return url, false
				}
				host, ok := hostParse(string(buffer), !url.isSpecial())
				if !ok {
					return url, false
				}
				url.host = &host
				buffer = buffer[:0]
				state = statePathStart
				if stateOverride != stateNone {
					return url, true
				}
			} else {
				if c == '[' {
					insideBrackets = true
				}
				if c == ']' {
					insideBrackets = false
				}
				buffer = append(buffer, c)
			}

		case statePort:
			if isASCIIDigit(c) {
				buffer = append(buffer, c)
			} else if c == eof || c == '/' || c == '?' || c == '#' || (url.isSpecial() && c == '\\') || stateOverride != stateNone {
				if len(buffer) != 0 {
					port := 0
					for _, d := range buffer {
						port = port*10 + int(d-'0')
						if port > 65535 {
							return url, false
						}
					}
					if port == defaultPort(url.scheme) {
						url.port = -1
					} else {
						url.port = port
					}
					buffer = buffer[:0]
					if stateOverride != stateNone {
						return url, true
					}
				}
				if stateOverride != stateNone {
					return url, false
				}
				state = statePathStart
				pointer--
			} else {
				return url, false
			}

		case stateFile:
			url.scheme = "file"
			url.host = strPtr("")
			if c == '/' || c == '\\' {
				state = stateFileSlash
			} else if base != nil && base.scheme == "file" {
				url.host = base.host
				url.path = append([]string(nil), base.path...)
				url.query = base.query
				if c == '?' {
					url.query = strPtr("")
					state = stateQuery
				} else if c == '#' {
					url.fragment = strPtr("")
					state = stateFragment
				} else if c != eof {
					url.query = nil
					if !startsWithWindowsDriveLetter(runes[pointer:]) {
						url.shortenPath()
					} else {
						url.path = nil
					}
					state = statePath
					pointer--
				}
			} else {
				state = statePath
				pointer--
			}

		case stateFileSlash:
			if c == '/' || c == '\\' {
				state = stateFileHost
			} else {
				if base != nil && base.scheme == "file" {
					url.host = base.host
					if !startsWithWindowsDriveLetter(runesFrom(runes, pointer)) && len(base.path) > 0 && isNormalizedWindowsDriveLetter(base.path[0]) {
						url.path = append(url.path, base.path[0])
					}
				}
				state = statePath
				pointer--
			}

		case stateFileHost:
			if c == eof || c == '/' || c == '\\' || c == '?' || c == '#' {
				pointer--
				if stateOverride == stateNone && isWindowsDriveLetter(buffer) {
					state = statePath
				} else if len(buffer) == 0 {
					url.host = strPtr("")
					if stateOverride != stateNone {
						return url, true
					}
					state = statePathStart
				} else {
					host, ok := hostParse(string(buffer), !url.isSpecial())
					if !ok {
						return url, false
					}
					if host == "localhost" {
						host = ""
					}
					url.host = &host
					if stateOverride != stateNone {
						return url, true
					}
					buffer = buffer[:0]
					state = statePathStart
				}
			} else {
				buffer = append(buffer, c)
			}

		case statePathStart:
			if url.isSpecial() {
				state = statePath
				if c != '/' && c != '\\' {
					pointer--
				}
			} else if stateOverride == stateNone && c == '?' {
				url.query = strPtr("")
				state = stateQuery
			} else if stateOverride == stateNone && c == '#' {
				url.fragment = strPtr("")
				state = stateFragment
			} else if c != eof {
				state = statePath
				if c != '/' {
					pointer--
				}
			} else if stateOverride != stateNone && url.host == nil {
				url.path = append(url.path, "")
			}

		case statePath:
			slashLike := c == '/' || (url.isSpecial() && c == '\\')
			if c == eof || slashLike || (stateOverride == stateNone && (c == '?' || c == '#')) {
				bufStr := string(buffer)
				if isDoubleDotSegment(bufStr) {
					url.shortenPath()
					if !slashLike {
						url.path = append(url.path, "")
					}
				} else if isSingleDotSegment(bufStr) && !slashLike {
					url.path = append(url.path, "")
				} else if !isSingleDotSegment(bufStr) {
					if url.scheme == "file" && len(url.path) == 0 && isWindowsDriveLetter(buffer) {
						bufStr = string(buffer[0]) + ":"
					}
					url.path = append(url.path, bufStr)
				}
				buffer = buffer[:0]
				if c == '?' {
					url.query = strPtr("")
					state = stateQuery
				}
				if c == '#' {
					url.fragment = strPtr("")
					state = stateFragment
				}
			} else {
				var b strings.Builder
				utf8PercentEncodeRune(&b, c, inPathSet)
				buffer = append(buffer, []rune(b.String())...)
			}

		case stateOpaquePath:
			// The code points up to the next ?, # or EOF are consumed in one go, so the path is built once.
			if c != '?' && c != '#' && c != eof {
				var b strings.Builder
				b.WriteString(url.path[0])
				for c != '?' && c != '#' && c != eof {
					if c == ' ' {
						if remainingStartsWith("?") || remainingStartsWith("#") {
							b.WriteString("%20")
						} else {
							b.WriteByte(' ')
						}
					} else {
						utf8PercentEncodeRune(&b, c, inC0ControlSet)
					}
					pointer++
					c = at(pointer)
				}
				url.path[0] = b.String()
			}
			if c == '?' {
				url.query = strPtr("")
				state = stateQuery
			} else if c == '#' {
				url.fragment = strPtr("")
				state = stateFragment
			}

		case stateQuery:
			if (stateOverride == stateNone && c == '#') || c == eof {
				set := inQuerySet
				if url.isSpecial() {
					set = inSpecialQuerySet
				}
				q := ""
				if url.query != nil {
					q = *url.query
				}
				q += utf8PercentEncodeString(string(buffer), set)
				url.query = &q
				buffer = buffer[:0]
				if c == '#' {
					url.fragment = strPtr("")
					state = stateFragment
				}
			} else if c != eof {
				buffer = append(buffer, c)
			}

		case stateFragment:
			// The rest of the input is the fragment; it is consumed in one go, so it is built once.
			if c != eof {
				var b strings.Builder
				if url.fragment != nil {
					b.WriteString(*url.fragment)
				}
				for ; pointer < len(runes); pointer++ {
					utf8PercentEncodeRune(&b, runes[pointer], inFragmentSet)
				}
				f := b.String()
				url.fragment = &f
			}
		}

		if pointer >= len(runes) {
			break
		}
		pointer++
	}

	return url, true
}

func runesFrom(runes []rune, i int) []rune {
	if i < 0 || i >= len(runes) {
		return nil
	}
	return runes[i:]
}

// Href is the URL serializer (`url.href`).
func (u *URL) Href() string {
	return u.serialize(false)
}

func (u *URL) serialize(excludeFragment bool) string {
	var b strings.Builder
	b.WriteString(u.scheme)
	b.WriteByte(':')
	if u.host != nil {
		b.WriteString("//")
		if u.includesCredentials() {
			b.WriteString(u.username)
			if u.password != "" {
				b.WriteByte(':')
				b.WriteString(u.password)
			}
			b.WriteByte('@')
		}
		b.WriteString(*u.host)
		if u.port != -1 {
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(u.port))
		}
	}
	if u.host == nil && !u.opaque && len(u.path) > 1 && u.path[0] == "" {
		b.WriteString("/.")
	}
	b.WriteString(u.Pathname())
	if u.query != nil {
		b.WriteByte('?')
		b.WriteString(*u.query)
	}
	if !excludeFragment && u.fragment != nil {
		b.WriteByte('#')
		b.WriteString(*u.fragment)
	}
	return b.String()
}

// String is `url.toString()` (the href).
func (u *URL) String() string { return u.Href() }

// ToJSON is `url.toJSON()` (the href).
func (u *URL) ToJSON() string { return u.Href() }

// Protocol is `url.protocol`: the scheme followed by ":".
func (u *URL) Protocol() string { return u.scheme + ":" }

// Username is `url.username` (percent-encoded).
func (u *URL) Username() string { return u.username }

// Password is `url.password` (percent-encoded).
func (u *URL) Password() string { return u.password }

// Host is `url.host`: the hostname plus ":port" when there is a non-default port.
func (u *URL) Host() string {
	if u.host == nil {
		return ""
	}
	if u.port == -1 {
		return *u.host
	}
	return *u.host + ":" + strconv.Itoa(u.port)
}

// Hostname is `url.hostname`.
func (u *URL) Hostname() string {
	if u.host == nil {
		return ""
	}
	return *u.host
}

// Port is `url.port`; "" for the default or no port.
func (u *URL) Port() string {
	if u.port == -1 {
		return ""
	}
	return strconv.Itoa(u.port)
}

// Pathname is `url.pathname` (the URL path serializer).
func (u *URL) Pathname() string {
	if u.opaque {
		return u.path[0]
	}
	var b strings.Builder
	for _, segment := range u.path {
		b.WriteByte('/')
		b.WriteString(segment)
	}
	return b.String()
}

// Search is `url.search`: "" or "?" followed by the query.
func (u *URL) Search() string {
	if u.query == nil || *u.query == "" {
		return ""
	}
	return "?" + *u.query
}

// Hash is `url.hash`: "" or "#" followed by the fragment.
func (u *URL) Hash() string {
	if u.fragment == nil || *u.fragment == "" {
		return ""
	}
	return "#" + *u.fragment
}

// Origin is `url.origin` (ASCII serialization; "null" for opaque origins).
func (u *URL) Origin() string {
	switch u.scheme {
	case "blob":
		if inner, err := NewURL(u.Pathname()); err == nil && (inner.scheme == "http" || inner.scheme == "https") {
			return inner.Origin()
		}
		return "null"
	case "ftp", "http", "https", "ws", "wss":
		origin := u.scheme + "://" + *u.host
		if u.port != -1 {
			origin += ":" + strconv.Itoa(u.port)
		}
		return origin
	}
	return "null"
}

// SetHref is the `href` setter; an invalid value fails with the `Invalid URL` TypeError.
func (u *URL) SetHref(value string) error {
	parsed, ok := basicURLParse(value, nil, nil, stateNone)
	if !ok {
		return &InvalidURLError{Input: value}
	}
	*u = *parsed
	return nil
}

// stateOverrideParse runs the basic URL parser on u with a state override. As in the standard, the
// parser mutates u in place, so a failure part way (for example an invalid port in the host setter)
// keeps the changes made before it.
func (u *URL) stateOverrideParse(input string, state urlState) {
	basicURLParse(input, nil, u, state)
}

// SetProtocol is the `protocol` setter.
func (u *URL) SetProtocol(value string) {
	u.stateOverrideParse(value+":", stateSchemeStart)
}

// SetUsername is the `username` setter.
func (u *URL) SetUsername(value string) {
	if u.cannotHaveUsernamePasswordPort() {
		return
	}
	u.username = utf8PercentEncodeString(value, inUserinfoSet)
}

// SetPassword is the `password` setter.
func (u *URL) SetPassword(value string) {
	if u.cannotHaveUsernamePasswordPort() {
		return
	}
	u.password = utf8PercentEncodeString(value, inUserinfoSet)
}

// SetHost is the `host` setter.
func (u *URL) SetHost(value string) {
	if u.opaque {
		return
	}
	u.stateOverrideParse(value, stateHost)
}

// SetHostname is the `hostname` setter.
func (u *URL) SetHostname(value string) {
	if u.opaque {
		return
	}
	u.stateOverrideParse(value, stateHostname)
}

// SetPort is the `port` setter.
func (u *URL) SetPort(value string) {
	if u.cannotHaveUsernamePasswordPort() {
		return
	}
	if value == "" {
		u.port = -1
		return
	}
	u.stateOverrideParse(value, statePort)
}

// SetPathname is the `pathname` setter.
func (u *URL) SetPathname(value string) {
	if u.opaque {
		return
	}
	u.path = nil
	u.stateOverrideParse(value, statePathStart)
}

// SetSearch is the `search` setter.
func (u *URL) SetSearch(value string) {
	if value == "" {
		u.query = nil
		u.stripTrailingSpacesFromOpaquePath()
		return
	}
	input := strings.TrimPrefix(value, "?")
	u.query = strPtr("")
	u.stateOverrideParse(input, stateQuery)
}

// SetHash is the `hash` setter.
func (u *URL) SetHash(value string) {
	if value == "" {
		u.fragment = nil
		u.stripTrailingSpacesFromOpaquePath()
		return
	}
	input := strings.TrimPrefix(value, "#")
	u.fragment = strPtr("")
	u.stateOverrideParse(input, stateFragment)
}

// stripTrailingSpacesFromOpaquePath is "potentially strip trailing spaces from an opaque path".
func (u *URL) stripTrailingSpacesFromOpaquePath() {
	if !u.opaque || u.fragment != nil || u.query != nil {
		return
	}
	u.path[0] = strings.TrimRight(u.path[0], " ")
}
