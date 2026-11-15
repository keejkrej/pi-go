// Ported from packages/coding-agent/src/modes/interactive/components/session-selector-search.ts (pi v1.5.2).

package components

import (
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsre"
	"github.com/keejkrej/pi-go/tui"
)

// SortMode controls ordering in the session selector.
type SortMode string

const (
	SortModeThreaded  SortMode = "threaded"
	SortModeRecent    SortMode = "recent"
	SortModeRelevance SortMode = "relevance"
)

// NameFilter filters sessions by whether they have a display name.
type NameFilter string

const (
	NameFilterAll   NameFilter = "all"
	NameFilterNamed NameFilter = "named"
)

// SearchToken is a single parsed term from a session search query.
type SearchToken struct {
	Kind  string // "fuzzy" | "phrase"
	Value string
}

// ParsedSearchQuery is the result of ParseSearchQuery.
type ParsedSearchQuery struct {
	Mode   string // "tokens" | "regex"
	Tokens []SearchToken
	Regex  *jsre.Regexp
	Error  *string
}

// MatchResult describes whether and how well a session matched a query.
type MatchResult struct {
	Matches bool
	Score   float64
}

const (
	sssTierName   = 0
	sssTierText   = 1
	sssTierFuzzy  = 2
	sssTierWeight = 1_000_000.0
)

var sssWhitespaceRe = jsre.MustCompile(`\s+`, "")

func sssNormalizeWhitespaceLower(text string) string {
	return js.Trim(sssWhitespaceRe.Replace(js.ToLower(text), " "))
}

type sssLowercaseCache struct {
	source        string
	lower         string
	hasNormalized bool
	normalized    string
}

var (
	sssMsgCacheMu sync.Mutex
	sssMsgCache   = make(map[string]*sssLowercaseCache)
)

func sssGetMessageText(session core.SessionInfo) *sssLowercaseCache {
	key := session.Path + "\x00" + session.Id
	sssMsgCacheMu.Lock()
	cached := sssMsgCache[key]
	if cached != nil && cached.source == session.AllMessagesText {
		sssMsgCacheMu.Unlock()
		return cached
	}
	if len(sssMsgCache) >= 512 {
		clear(sssMsgCache)
	}
	sssMsgCacheMu.Unlock()

	fresh := &sssLowercaseCache{
		source: session.AllMessagesText,
		lower:  js.ToLower(session.AllMessagesText),
	}
	sssMsgCacheMu.Lock()
	sssMsgCache[key] = fresh
	sssMsgCacheMu.Unlock()
	return fresh
}

func sssGetNormalizedMessageText(session core.SessionInfo) string {
	cached := sssGetMessageText(session)
	sssMsgCacheMu.Lock()
	if !cached.hasNormalized {
		cached.normalized = js.Trim(sssWhitespaceRe.Replace(cached.lower, " "))
		cached.hasNormalized = true
	}
	out := cached.normalized
	sssMsgCacheMu.Unlock()
	return out
}

func sssTierScore(tier int, score float64) float64 {
	return float64(tier)*sssTierWeight + score
}

func sssSessionName(session core.SessionInfo) string {
	if session.Name == nil {
		return ""
	}
	return *session.Name
}

func sssMatchLiteralToken(session core.SessionInfo, value string) *float64 {
	name := js.ToLower(sssSessionName(session))
	if strings.Contains(name, value) {
		s := sssTierScore(sssTierName, tui.NewFuzzyMatch(value, name).Score)
		return &s
	}
	if strings.Contains(js.ToLower(session.Id), value) {
		s := sssTierScore(sssTierText, 0)
		return &s
	}
	if strings.Contains(sssGetMessageText(session).lower, value) {
		s := sssTierScore(sssTierText, 0)
		return &s
	}
	if strings.Contains(js.ToLower(session.Cwd), value) {
		s := sssTierScore(sssTierText, 0)
		return &s
	}
	return nil
}

func sssMatchFuzzyToken(session core.SessionInfo, value string) *float64 {
	if literal := sssMatchLiteralToken(session, js.ToLower(value)); literal != nil {
		return literal
	}
	var best *float64
	for _, field := range []string{sssSessionName(session), session.Cwd} {
		m := tui.NewFuzzyMatch(value, field)
		if m.Matches && (best == nil || m.Score < *best) {
			score := m.Score
			best = &score
		}
	}
	if best == nil {
		return nil
	}
	s := sssTierScore(sssTierFuzzy, *best)
	return &s
}

func sssMatchPhraseToken(session core.SessionInfo, phrase string) *float64 {
	name := sssNormalizeWhitespaceLower(sssSessionName(session))
	if strings.Contains(name, phrase) {
		s := sssTierScore(sssTierName, tui.NewFuzzyMatch(phrase, name).Score)
		return &s
	}
	if strings.Contains(js.ToLower(session.Id), phrase) {
		s := sssTierScore(sssTierText, 0)
		return &s
	}
	if strings.Contains(sssGetNormalizedMessageText(session), phrase) {
		s := sssTierScore(sssTierText, 0)
		return &s
	}
	if strings.Contains(sssNormalizeWhitespaceLower(session.Cwd), phrase) {
		s := sssTierScore(sssTierText, 0)
		return &s
	}
	return nil
}

func sssMatchRegex(session core.SessionInfo, regex *jsre.Regexp) *float64 {
	if name := sssSessionName(session); name != "" && regex.Test(name) {
		s := sssTierScore(sssTierName, 0)
		return &s
	}
	if regex.Test(session.Id) || regex.Test(session.AllMessagesText) || regex.Test(session.Cwd) {
		s := sssTierScore(sssTierText, 0)
		return &s
	}
	return nil
}

// HasSessionName reports whether session has a non-whitespace display name.
func HasSessionName(session core.SessionInfo) bool {
	return session.Name != nil && js.Trim(*session.Name) != ""
}

func sssMatchesNameFilter(session core.SessionInfo, filter NameFilter) bool {
	if filter == NameFilterAll {
		return true
	}
	return HasSessionName(session)
}

func sssSplitWhitespaceTokens(s string) []SearchToken {
	parts := sssWhitespaceRe.Split(s, -1)
	tokens := make([]SearchToken, 0, len(parts))
	for _, part := range parts {
		trimmed := js.Trim(part)
		if trimmed != "" {
			tokens = append(tokens, SearchToken{Kind: "fuzzy", Value: trimmed})
		}
	}
	return tokens
}

// ParseSearchQuery parses a session search string into token or re:<pattern> mode.
func ParseSearchQuery(query string) ParsedSearchQuery {
	trimmed := js.Trim(query)
	if trimmed == "" {
		return ParsedSearchQuery{Mode: "tokens"}
	}

	if strings.HasPrefix(trimmed, "re:") {
		pattern := js.Trim(trimmed[3:])
		if pattern == "" {
			errMsg := "Empty regex"
			return ParsedSearchQuery{Mode: "regex", Error: &errMsg}
		}
		re, err := jsre.Compile(pattern, "i")
		if err != nil {
			msg := err.Error()
			return ParsedSearchQuery{Mode: "regex", Error: &msg}
		}
		return ParsedSearchQuery{Mode: "regex", Regex: re}
	}

	var tokens []SearchToken
	var buf strings.Builder
	inQuote := false
	hadUnclosedQuote := false

	flush := func(kind string) {
		v := js.Trim(buf.String())
		buf.Reset()
		if v == "" {
			return
		}
		tokens = append(tokens, SearchToken{Kind: kind, Value: v})
	}

	for i := 0; i < len(trimmed); {
		r, w := utf8.DecodeRuneInString(trimmed[i:])
		i += w
		if r == '"' {
			if inQuote {
				flush("phrase")
				inQuote = false
			} else {
				flush("fuzzy")
				inQuote = true
			}
			continue
		}
		if !inQuote && js.IsJSWhitespace(r) {
			flush("fuzzy")
			continue
		}
		buf.WriteRune(r)
	}

	if inQuote {
		hadUnclosedQuote = true
	}

	if hadUnclosedQuote {
		return ParsedSearchQuery{
			Mode:   "tokens",
			Tokens: sssSplitWhitespaceTokens(trimmed),
		}
	}

	flush("fuzzy")
	return ParsedSearchQuery{Mode: "tokens", Tokens: tokens}
}

// MatchSession evaluates a parsed search query against one session.
func MatchSession(session core.SessionInfo, parsed ParsedSearchQuery) MatchResult {
	if parsed.Mode == "regex" {
		if parsed.Regex == nil {
			return MatchResult{Matches: false}
		}
		score := sssMatchRegex(session, parsed.Regex)
		if score == nil {
			return MatchResult{Matches: false}
		}
		return MatchResult{Matches: true, Score: *score}
	}

	totalScore := 0.0
	for _, token := range parsed.Tokens {
		var score *float64
		if token.Kind == "phrase" {
			phrase := sssNormalizeWhitespaceLower(token.Value)
			if phrase == "" {
				continue
			}
			score = sssMatchPhraseToken(session, phrase)
		} else {
			score = sssMatchFuzzyToken(session, token.Value)
		}
		if score == nil {
			return MatchResult{Matches: false}
		}
		totalScore += *score
	}
	return MatchResult{Matches: true, Score: totalScore}
}

// FilterAndSortSessions applies nameFilter, search query filtering, and sortMode ordering.
func FilterAndSortSessions(
	sessions []core.SessionInfo,
	query string,
	sortMode SortMode,
	nameFilter ...NameFilter,
) []core.SessionInfo {
	nf := NameFilterAll
	if len(nameFilter) > 0 && nameFilter[0] != "" {
		nf = nameFilter[0]
	}

	var nameFiltered []core.SessionInfo
	if nf == NameFilterAll {
		nameFiltered = sessions
	} else {
		for _, s := range sessions {
			if sssMatchesNameFilter(s, nf) {
				nameFiltered = append(nameFiltered, s)
			}
		}
	}

	if js.Trim(query) == "" {
		return nameFiltered
	}

	parsed := ParseSearchQuery(query)
	if parsed.Error != nil {
		return nil
	}

	if sortMode == SortModeRecent {
		var filtered []core.SessionInfo
		for _, s := range nameFiltered {
			if MatchSession(s, parsed).Matches {
				filtered = append(filtered, s)
			}
		}
		return filtered
	}

	type scoredSession struct {
		session core.SessionInfo
		score   float64
	}
	var scored []scoredSession
	for _, s := range nameFiltered {
		res := MatchSession(s, parsed)
		if res.Matches {
			scored = append(scored, scoredSession{session: s, score: res.Score})
		}
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score < scored[j].score
		}
		return scored[i].session.Modified.UnixMilli() > scored[j].session.Modified.UnixMilli()
	})

	out := make([]core.SessionInfo, len(scored))
	for i, item := range scored {
		out[i] = item.session
	}
	return out
}
