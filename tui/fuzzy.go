// Ported from packages/tui/src/fuzzy.ts (pi v1.0.0).

package tui

import (
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsre"
)

// FuzzyMatch is the result of a fuzzy comparison. Lower Score is a better match.
type FuzzyMatch struct {
	Matches bool    `json:"matches"`
	Score   float64 `json:"score"`
}

var (
	fuzzyWordBoundaryRe = jsre.MustCompile(`[\s\-_./:]`, "")
	fuzzyAlphaNumRe     = jsre.MustCompile(`^(?<letters>[a-z]+)(?<digits>[0-9]+)$`, "")
	fuzzyNumAlphaRe     = jsre.MustCompile(`^(?<digits>[0-9]+)(?<letters>[a-z]+)$`, "")
	fuzzyTokenSplitRe   = jsre.MustCompile(`[\s/]+`, "")
)

func fuzzyToUTF16(s string) []uint16 {
	return utf16.Encode([]rune(s))
}

func fuzzyScorePositions(textUnits []uint16, positions []int) float64 {
	score := 0.0
	lastMatchIndex := -1
	consecutiveMatches := 0

	for _, i := range positions {
		// Reward consecutive matches
		if lastMatchIndex == i-1 {
			consecutiveMatches++
			score -= float64(consecutiveMatches * 5)
		} else {
			consecutiveMatches = 0
			// Penalize gaps
			if lastMatchIndex >= 0 {
				score += float64((i - lastMatchIndex - 1) * 2)
			}
		}

		// Reward word boundary matches
		if i == 0 {
			score -= 10
		} else {
			prev := string(utf16.Decode([]uint16{textUnits[i-1]}))
			if fuzzyWordBoundaryRe.Test(prev) {
				score -= 10
			}
		}

		// Slight penalty for later matches
		score += float64(i) * 0.1

		lastMatchIndex = i
	}

	return score
}

func fuzzyIndexOfUnit(haystack []uint16, needle uint16, from int) int {
	for i := from; i < len(haystack); i++ {
		if haystack[i] == needle {
			return i
		}
	}
	return -1
}

func fuzzyIndexOfSubslice(haystack []uint16, needle []uint16, from int) int {
	if len(needle) == 0 {
		return from
	}
	limit := len(haystack) - len(needle)
	for i := from; i <= limit; i++ {
		matched := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				matched = false
				break
			}
		}
		if matched {
			return i
		}
	}
	return -1
}

func fuzzyMatchQuery(normalizedQuery, textLower string) FuzzyMatch {
	queryUnits := fuzzyToUTF16(normalizedQuery)
	if len(queryUnits) == 0 {
		return FuzzyMatch{Matches: true, Score: 0}
	}

	textUnits := fuzzyToUTF16(textLower)
	if len(queryUnits) > len(textUnits) {
		return FuzzyMatch{Matches: false, Score: 0}
	}

	// Greedy alignment: each query character at its first occurrence after the previous one.
	positions := make([]int, 0, len(queryUnits))
	lastMatchIndex := -1
	for _, ch := range queryUnits {
		i := fuzzyIndexOfUnit(textUnits, ch, lastMatchIndex+1)
		if i == -1 {
			return FuzzyMatch{Matches: false, Score: 0}
		}
		positions = append(positions, i)
		lastMatchIndex = i
	}

	score := fuzzyScorePositions(textUnits, positions)

	// The greedy alignment can scatter the query over an earlier, unrelated part of the text,
	// e.g. "opus" in "openrouter openrouter/anthropic/claude-opus" takes o, p, u from "openrouter".
	// Also score every contiguous occurrence and keep the best alignment.
	for start := fuzzyIndexOfSubslice(textUnits, queryUnits, 0); start != -1; start = fuzzyIndexOfSubslice(textUnits, queryUnits, start+1) {
		for k := range positions {
			positions[k] = start + k
		}
		candidate := fuzzyScorePositions(textUnits, positions)
		if candidate < score {
			score = candidate
		}
	}

	if normalizedQuery == textLower {
		score -= 100
	}

	return FuzzyMatch{Matches: true, Score: score}
}

// NewFuzzyMatch reports whether every query character appears in text in order.
// It is TS fuzzyMatch. The function is NewFuzzyMatch because the result type keeps the name FuzzyMatch.
func NewFuzzyMatch(query string, text string) FuzzyMatch {
	queryLower := strings.ToLower(query)
	textLower := strings.ToLower(text)

	primaryMatch := fuzzyMatchQuery(queryLower, textLower)
	if primaryMatch.Matches {
		return primaryMatch
	}

	var swappedQuery string
	if m := fuzzyAlphaNumRe.Exec(queryLower, 0); m != nil {
		swappedQuery = m.Named("digits") + m.Named("letters")
	} else if m := fuzzyNumAlphaRe.Exec(queryLower, 0); m != nil {
		swappedQuery = m.Named("letters") + m.Named("digits")
	}

	if swappedQuery == "" {
		return primaryMatch
	}

	swappedMatch := fuzzyMatchQuery(swappedQuery, textLower)
	if !swappedMatch.Matches {
		return primaryMatch
	}

	return FuzzyMatch{Matches: true, Score: swappedMatch.Score + 5}
}

// FuzzyFilter returns the items whose text matches every whitespace- or slash-separated
// query token, best match first. A query that is empty after trim returns items unchanged.
func FuzzyFilter[T any](items []T, query string, getText func(item T) string) []T {
	trimmed := js.Trim(query)
	if trimmed == "" {
		return items
	}

	rawTokens := fuzzyTokenSplitRe.Split(trimmed, -1)
	tokens := make([]string, 0, len(rawTokens))
	for _, t := range rawTokens {
		if len(t) > 0 {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 {
		return items
	}

	type scoredItem struct {
		item       T
		totalScore float64
	}
	results := make([]scoredItem, 0, len(items))

	for _, item := range items {
		text := getText(item)
		totalScore := 0.0
		allMatch := true
		for _, token := range tokens {
			match := NewFuzzyMatch(token, text)
			if match.Matches {
				totalScore += match.Score
			} else {
				allMatch = false
				break
			}
		}
		if allMatch {
			results = append(results, scoredItem{item: item, totalScore: totalScore})
		}
	}

	slices.SortStableFunc(results, func(a, b scoredItem) int {
		if a.totalScore < b.totalScore {
			return -1
		}
		if a.totalScore > b.totalScore {
			return 1
		}
		return 0
	})

	out := make([]T, len(results))
	for i, r := range results {
		out[i] = r.item
	}
	return out
}
