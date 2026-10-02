// Ported from packages/tui/src/fuzzy.ts (pi v1.0.0).

package tui

// FuzzyMatch is the result of a fuzzy comparison. Lower Score is a better match.
type FuzzyMatch struct {
	Matches bool    `json:"matches"`
	Score   float64 `json:"score"`
}

// NewFuzzyMatch reports whether every query character appears in text in order.
// It is TS fuzzyMatch. The function is NewFuzzyMatch because the result type keeps the name FuzzyMatch.
func NewFuzzyMatch(query string, text string) FuzzyMatch {
	panic("unported: NewFuzzyMatch")
}

// FuzzyFilter returns the items whose text matches every whitespace- or slash-separated
// query token, best match first. A query that is empty after trim returns items unchanged.
func FuzzyFilter[T any](items []T, query string, getText func(item T) string) []T {
	panic("unported: FuzzyFilter")
}
