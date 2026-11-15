// Ported from packages/ai/src/utils/headers.ts (pi v1.5.2).

package ai

import (
	"net/http"
	"strings"

	"github.com/keejkrej/pi-go/internal/omap"
)

type uheHeaderEntry struct {
	name  string
	value string
}

// HeadersToRecord converts HTTP response headers into a lower-cased single-string map.
func HeadersToRecord(headers http.Header) map[string]string {
	result := make(map[string]string, len(headers))
	for k, vals := range headers {
		result[strings.ToLower(k)] = strings.Join(vals, ", ")
	}
	return result
}

// ProviderHeadersToRecord merges provider header sources case-insensitively.
// Later sources override earlier ones, nil pointer values delete earlier entries,
// and nil is returned if the merged result is empty.
func ProviderHeadersToRecord(headerSources ...ProviderHeaders) map[string]string {
	merged := omap.NewMap[string, uheHeaderEntry]()
	for _, source := range headerSources {
		if source == nil {
			continue
		}
		for name, value := range source.All() {
			normalized := strings.ToLower(name)
			merged.Delete(normalized)
			if value != nil {
				merged.Set(normalized, uheHeaderEntry{name: name, value: *value})
			}
		}
	}
	if merged.Len() == 0 {
		return nil
	}
	out := make(map[string]string, merged.Len())
	for _, entry := range merged.All() {
		out[entry.name] = entry.value
	}
	return out
}

// ProviderHeadersToOrderedRecord merges provider header sources preserving insertion order.
func ProviderHeadersToOrderedRecord(headerSources ...ProviderHeaders) *omap.Map[string, string] {
	merged := omap.NewMap[string, uheHeaderEntry]()
	for _, source := range headerSources {
		if source == nil {
			continue
		}
		for name, value := range source.All() {
			normalized := strings.ToLower(name)
			merged.Delete(normalized)
			if value != nil {
				merged.Set(normalized, uheHeaderEntry{name: name, value: *value})
			}
		}
	}
	if merged.Len() == 0 {
		return nil
	}
	out := omap.NewMap[string, string]()
	for _, entry := range merged.All() {
		out.Set(entry.name, entry.value)
	}
	return out
}
