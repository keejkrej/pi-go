// Ported from packages/coding-agent/src/modes/interactive/model-search.ts (pi v1.5.2).

package components

import (
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsre"
)

// ModelSearchItem holds the fields used when building searchable text or display names for a model.
type ModelSearchItem struct {
	Id       string
	Provider string
	Name     *string
}

var msNonAlphaNumRe = jsre.MustCompile(`[^\p{L}\p{N}]+`, "u")

// GetModelSearchText builds the default searchable string for a model.
func GetModelSearchText(item ModelSearchItem) string {
	name := ""
	if item.Name != nil && *item.Name != "" {
		name = " " + *item.Name
	}
	return item.Id + " " + item.Provider + " " + item.Provider + "/" + item.Id + " " + item.Provider + " " + item.Id + name
}

func msSplitModelLabel(value string) []string {
	parts := msNonAlphaNumRe.Split(js.ToLower(value), -1)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// GetDistinctModelName returns the trimmed model name when it introduces words that the ID does not cover.
func GetDistinctModelName(item ModelSearchItem) *string {
	if item.Name == nil {
		return nil
	}
	name := js.Trim(*item.Name)
	if name == "" {
		return nil
	}
	idWords := msSplitModelLabel(item.Id)
	idTokens := make(map[string]struct{}, len(idWords))
	for _, w := range idWords {
		idTokens[w] = struct{}{}
	}
	compactId := strings.Join(idWords, "")
	// Single-character words must match a whole ID token: "4" is not covered by "mistral-small-2603".
	for _, word := range msSplitModelLabel(name) {
		if _, ok := idTokens[word]; ok {
			continue
		}
		if len([]rune(word)) > 1 && strings.Contains(compactId, word) {
			continue
		}
		return &name
	}
	return nil
}

// GetModelSelectorSearchText builds the search text for /model so exact provider-prefixed queries rank above proxy-provider IDs.
func GetModelSelectorSearchText(item ModelSearchItem) string {
	name := ""
	if item.Name != nil && *item.Name != "" {
		name = " " + *item.Name
	}
	return item.Provider + " " + item.Provider + "/" + item.Id + " " + item.Provider + " " + item.Id + name
}
