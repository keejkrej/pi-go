package typebox

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/js"
	"golang.org/x/net/idna"
)

func formatOK(name, value string) bool {
	switch name {
	case "date-time":
		return isDateTime(value)
	case "date":
		return isDate(value)
	case "time":
		return isTime(value)
	case "duration":
		return formatMatch(formatDuration, formatDurationFlags, value)
	case "email":
		return formatMatch(formatEmail, formatEmailFlags, value)
	case "idn-email":
		return formatMatch(formatIDNEmail, formatIDNEmailFlags, value)
	case "hostname":
		return isHostname(value)
	case "idn-hostname":
		return isIDNHostname(value)
	case "ipv4":
		return formatMatch(formatIPv4, formatIPv4Flags, value)
	case "ipv6":
		return formatMatch(formatIPv6, formatIPv6Flags, value)
	case "uri":
		return formatMatch(formatURI, formatURIFlags, value)
	case "uri-reference":
		return formatMatch(formatURIReference, formatURIReferenceFlags, value)
	case "uri-template":
		return formatMatch(formatURITemplate, formatURITemplateFlags, value)
	case "uuid":
		return formatMatch(formatUUID, formatUUIDFlags, value)
	case "json-pointer":
		return formatMatch(formatJSONPointer, formatJSONPointerFlags, value)
	case "json-pointer-uri-fragment":
		return formatMatch(formatJSONPointerURIFragment, formatJSONPointerURIFragmentFlags, value)
	case "relative-json-pointer":
		return formatMatch(formatRelativeJSONPointer, formatRelativeJSONPointerFlags, value)
	case "regex":
		_, err := compileRE(value, "u")
		return err == nil
	case "url":
		return urlCanParse(value, "")
	case "iri":
		return isIRI(value)
	case "iri-reference":
		return isIRIReference(value)
	default:
		return true
	}
}

func formatMatch(pattern, flags, value string) bool {
	ok, err := reTest(pattern, flags, value)
	return err == nil && ok
}

var (
	reDate      = mustRE(`^(\d\d\d\d)-(\d\d)-(\d\d)$`, "")
	reTime      = mustRE(`^(\d\d):(\d\d):(\d\d)(?:\.\d+)?(?:([Zz])|([+-])(\d\d):(\d\d))?$`, "")
	reSplitT    = mustRE(`T`, "i")
	daysInMonth = [...]int{0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
)

func isDate(value string) bool {
	m := reDate.Exec(value, 0)
	if m == nil {
		return false
	}
	year := atoi(m.Group(1))
	month := atoi(m.Group(2))
	day := atoi(m.Group(3))
	if month < 1 || month > 12 || day < 1 {
		return false
	}
	limit := daysInMonth[month]
	if month == 2 && isLeapYear(year) {
		limit = 29
	}
	return day <= limit
}

func isLeapYear(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func isTime(value string) bool {
	m := reTime.Exec(value, 0)
	if m == nil {
		return false
	}
	z, zok := m.GroupOK(4)
	sign, signOK := m.GroupOK(5)
	if !zok && !signOK {
		return false
	}
	_ = z
	hr := atoi(m.Group(1))
	min := atoi(m.Group(2))
	sec := atoi(m.Group(3))
	if hr > 23 || min > 59 || sec > 60 {
		return false
	}
	tzh, tzm := 0, 0
	if signOK {
		hs, hok := m.GroupOK(6)
		ms, mok := m.GroupOK(7)
		if hok {
			tzh = atoi(hs)
		}
		if mok {
			tzm = atoi(ms)
		}
		if tzh > 23 || tzm > 59 {
			return false
		}
	}
	if sec < 60 {
		return true
	}
	tzSign := 1
	if sign == "-" {
		tzSign = -1
	}
	total := (hr*60 + min) - tzSign*(tzh*60+tzm)
	return ((total%1440)+1440)%1440 == 1439
}

func isDateTime(value string) bool {
	parts := reSplitT.Split(value, -1)
	return len(parts) == 2 && isDate(parts[0]) && isTime(parts[1])
}

func isHostname(value string) bool {
	n := js.Len(value)
	if n == 0 || n > 253 {
		return false
	}
	if js.CharCodeAt(value, n-1) == 46 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if !isHostLabel(label) {
			return false
		}
	}
	return true
}

func isHostLabel(value string) bool {
	n := js.Len(value)
	if n == 0 || n > 63 {
		return false
	}
	return isPunyLabel(value) || isASCIILabel(value)
}

var (
	reHyphen        = mustRE(`^(?!-).*(?<!-)$`, "")
	reACE           = mustRE(`^(?!..--)`, "")
	reLDH           = mustRE(`^[a-zA-Z0-9-]*$`, "")
	reNonASCII      = mustRE(`[^\p{ASCII}]`, "u")
	reCombining     = mustRE(`[\p{Mn}\p{Mc}\p{Me}]`, "u")
	reDisallowed    = mustRE(`[\u{0640}\u{07fa}\u{302e}\u{302f}\u{3031}\u{3032}\u{3033}\u{3034}\u{3035}\u{303b}]`, "u")
	rePermitted     = mustRE(`\p{L}|[\u{002d}\u{002b}]|[\u{002e}\u{002c}\u{003a}\u{002f}]|\p{Nd}|\p{Mn}|\p{Mc}|[\u{00b7}\u{0375}\u{05f3}\u{05f4}\u{200c}\u{200d}\u{30fb}]|[\u{00df}\u{03c2}\u{06fd}\u{06fe}\u{0f0b}\u{3007}]`, "u")
	reGreek         = mustRE(`\p{Script=Greek}`, "u")
	reHebrew        = mustRE(`\p{Script=Hebrew}`, "u")
	reJapanese      = mustRE(`[\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Han}]`, "u")
	reArabicLetter  = mustRE(`[\p{Script=Arabic}\p{Script=Syriac}\p{Script=Thaana}\p{Script=Mandaic}]`, "u")
	reVirama        = mustRE(`[\u{094d}\u{09cd}\u{0a4d}\u{0acd}\u{0b4d}\u{0bcd}\u{0c4d}\u{0ccd}\u{0d3b}\u{0d3c}\u{0d4d}\u{0dca}\u{1b44}\u{1baa}\u{1bab}\u{a9c0}\u{11046}\u{1107f}\u{110b9}\u{11133}\u{11134}\u{111c0}\u{11235}\u{1134d}\u{11442}\u{114c2}\u{115bf}\u{1163f}\u{116b6}\u{11c3f}\u{11d44}\u{11d45}]`, "u")
	reEN            = mustRE(`[0-9]|[\u{06f0}-\u{06f9}]`, "u")
	reAN            = mustRE(`[\u{0660}-\u{0669}]`, "u")
	reNSM           = mustRE(`\p{Mn}`, "u")
	reLetter        = mustRE(`\p{L}`, "u")
	reIgnored       = mustRE(`[\u00ad\u034f\u180b-\u180d\u200b\ufe00-\ufe0f\u{e0100}-\u{e01ef}]`, "gu")
	reIPvFuture     = mustRE(`\[[vV][0-9a-fA-F]+\.[^\]]+\]`, "")
	reInvalidIRI    = mustRE(`[\x00-\x20<>\^`+"`"+`{|}\\]`, "")
	reBadPct        = mustRE(`%(?![0-9a-fA-F]{2})`, "")
	reInvalidIRIRef = mustRE(`[\x00-\x20\x7F\\]|%(?![0-9a-fA-F]{2})`, "")
	reBadScheme     = mustRE(`^[a-zA-Z][a-zA-Z0-9+\-.]*\/\/`, "")
)

func isASCIILabel(value string) bool {
	return reHyphen.Test(value) && reACE.Test(value) && reLDH.Test(value)
}

func isPunyLabel(value string) bool {
	if !strings.HasPrefix(strings.ToLower(value), "xn--") {
		return false
	}
	body := strings.ToLower(value[4:])
	if strings.LastIndex(body, "-") == 0 {
		return false
	}
	decoded, err := idna.ToUnicode(strings.ToLower(value))
	if err != nil || decoded == "" {
		return false
	}
	if !reNonASCII.Test(decoded) {
		return false
	}
	return isUnicodeLabel(decoded)
}

func exceedsALabel(value string) bool {
	if !reNonASCII.Test(value) {
		return false
	}
	ascii, err := idna.ToASCII(value)
	if err != nil {
		return true
	}
	return len(ascii) > 63
}

func isUnicodeLabel(value string) bool {
	if exceedsALabel(value) {
		return false
	}
	if hasRTL(value) && !satisfiesBidi(value) {
		return false
	}
	chars := []rune(value)
	if len(chars) == 0 {
		return false
	}
	if chars[0] == '-' || chars[len(chars)-1] == '-' {
		return false
	}
	if len(chars) >= 4 && chars[2] == '-' && chars[3] == '-' {
		return false
	}
	if reCombining.Test(string(chars[0])) {
		return false
	}
	cps := make([]int, len(chars))
	for i, r := range chars {
		cps[i] = int(r)
	}
	hasJapanese := false
	for i, cp := range cps {
		ch := string(chars[i])
		if reDisallowed.Test(ch) || !rePermitted.Test(ch) {
			return false
		}
		if reJapanese.Test(ch) {
			hasJapanese = true
		}
		var prev, next int
		var hasPrev, hasNext bool
		if i > 0 {
			prev = cps[i-1]
			hasPrev = true
		}
		if i+1 < len(cps) {
			next = cps[i+1]
			hasNext = true
		}
		switch cp {
		case 0x00b7:
			if prev != 0x006c || next != 0x006c {
				return false
			}
		case 0x0375:
			if !hasNext || !reGreek.Test(string(chars[i+1])) {
				return false
			}
		case 0x05f3, 0x05f4:
			if !hasPrev || !reHebrew.Test(string(chars[i-1])) {
				return false
			}
		case 0x200c:
			if !hasPrev || (prev < 0x0080 && !reVirama.Test(string(chars[i-1]))) {
				return false
			}
		case 0x200d:
			if !hasPrev || !reVirama.Test(string(chars[i-1])) {
				return false
			}
		}
		_ = hasPrev
	}
	if strings.ContainsRune(value, '\u30fb') && !hasJapanese {
		return false
	}
	return true
}

func bidiClass(r rune) string {
	ch := string(r)
	switch {
	case reEN.Test(ch):
		return "EN"
	case reAN.Test(ch):
		return "AN"
	case reNSM.Test(ch):
		return "NSM"
	case reHebrew.Test(ch):
		return "R"
	case reArabicLetter.Test(ch):
		return "AL"
	case reLetter.Test(ch):
		return "L"
	default:
		return "ON"
	}
}

func hasRTL(value string) bool {
	for _, r := range value {
		c := bidiClass(r)
		if c == "R" || c == "AL" || c == "AN" {
			return true
		}
	}
	return false
}

func bidiAllowed(rtl bool, class string) bool {
	if rtl {
		switch class {
		case "R", "AL", "AN", "EN", "ES", "CS", "ET", "ON", "BN", "NSM":
			return true
		}
		return false
	}
	switch class {
	case "L", "EN", "ES", "CS", "ET", "ON", "BN", "NSM":
		return true
	}
	return false
}

func satisfiesBidi(value string) bool {
	rtl := false
	sawEN, sawAN := false, false
	first := true
	for _, r := range value {
		class := bidiClass(r)
		if first {
			if class != "L" && class != "R" && class != "AL" {
				return false
			}
			rtl = class == "R" || class == "AL"
			first = false
		}
		if !bidiAllowed(rtl, class) {
			return false
		}
		if class == "EN" {
			sawEN = true
		} else if class == "AN" {
			sawAN = true
		}
	}
	if rtl && sawEN && sawAN {
		return false
	}
	return true
}

func hasBidiChars(label string) bool {
	if strings.HasPrefix(strings.ToLower(label), "xn--") {
		decoded, err := idna.ToUnicode(strings.ToLower(label))
		if err != nil {
			return false
		}
		return hasRTL(decoded)
	}
	return hasRTL(label)
}

func normalizeHost(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= 0xFF01 && r <= 0xFF5E {
			r -= 0xFEE0
		}
		b.WriteRune(r)
	}
	s := js.Normalize(b.String(), "NFC")
	s = reIgnored.Replace(s, "")
	var out strings.Builder
	for _, r := range s {
		switch r {
		case 0x002E, 0x3002, 0xFF0E, 0xFF61:
			out.WriteByte('.')
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}

func isIDNHostname(value string) bool {
	if value == "" || strings.Contains(value, " ") {
		return false
	}
	normalized := normalizeHost(value)
	if js.Len(normalized) > 253 {
		return false
	}
	labels := strings.Split(normalized, ".")
	bidi := false
	for _, label := range labels {
		if hasBidiChars(label) {
			bidi = true
			break
		}
	}
	for _, label := range labels {
		n := js.Len(label)
		if n == 0 || n > 63 || !(isPunyLabel(label) || isUnicodeLabel(label)) {
			return false
		}
		if bidi && !satisfiesBidi(label) {
			return false
		}
	}
	return true
}

func trimURLSpace(s string) string {
	return strings.Trim(s, "\u0000\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008\u0009\u000a\u000b\u000c\u000d\u000e\u000f\u0010\u0011\u0012\u0013\u0014\u0015\u0016\u0017\u0018\u0019\u001a\u001b\u001c\u001d\u001e\u001f ")
}

func urlCanParse(input, base string) bool {
	input = trimURLSpace(input)
	if base != "" {
		base = trimURLSpace(base)
	}
	if strings.ContainsAny(input, "\t\n\r") {
		input = strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(input)
	}
	if input == "" && base == "" {
		return false
	}
	if base == "" {
		u, err := url.Parse(input)
		if err != nil || u.Scheme == "" {
			return false
		}
		if !validParsedURL(u) {
			return false
		}
		return true
	}
	bu, err := url.Parse(base)
	if err != nil || bu.Scheme == "" {
		return false
	}
	rel, err := url.Parse(input)
	if err != nil {
		return false
	}
	u := bu.ResolveReference(rel)
	return u.Scheme != "" && validParsedURL(u)
}

func validParsedURL(u *url.URL) bool {
	if u == nil || u.Scheme == "" {
		return false
	}
	for _, r := range u.Scheme {
		if r > utf8.RuneSelf && r < 0x80 {
			return false
		}
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "ws", "wss", "ftp":
		return u.Host != ""
	default:
		return true
	}
}

func isIRI(value string) bool {
	if reInvalidIRI.Test(value) || reBadPct.Test(value) {
		return false
	}
	narrowed := value
	if js.Len(value) < 2048 {
		narrowed = reIPvFuture.Replace(value, "[::1]")
	}
	return urlCanParse(narrowed, "")
}

func isIRIReference(value string) bool {
	if reInvalidIRIRef.Test(value) || reBadScheme.Test(value) {
		return false
	}
	return urlCanParse(value, "http://example.com")
}
