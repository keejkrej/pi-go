// Ported from packages/telemetry/src/render.ts (pi v1.5.2).

package telemetry

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/omap"
)

// renderEscaped writes text with every C0/C1 control character and DEL written as \uXXXX.
// A stored id, name or message may come from another process (a carrier), so none reaches
// a terminal or a viewer raw.
func renderEscaped(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(utf8.RuneError)
			i++
			continue
		}
		if (r >= 0x0000 && r <= 0x001f) || (r >= 0x007f && r <= 0x009f) {
			fmt.Fprintf(&b, "\\u%04x", r)
		} else {
			b.WriteString(text[i : i+size])
		}
		i += size
	}
	return b.String()
}

// renderMicroseconds returns ms rounded to the microsecond, as the adapters record span times.
func renderMicroseconds(ms float64) float64 {
	return js.Round(ms*1000) / 1000
}

func renderFormatAttributes(attributes SpanAttributes) string {
	if attributes == nil || attributes.Len() == 0 {
		return ""
	}
	keys, err := attributes.ObjectKeys()
	if err != nil {
		keys = attributes.Keys()
	}
	var b strings.Builder
	for _, key := range keys {
		val, _ := attributes.Get(key)
		encoded, err := jsonx.Stringify(val)
		if err != nil {
			encoded = "null"
		} else if encoded == "" {
			encoded = "undefined"
		}
		b.WriteByte(' ')
		b.WriteString(renderEscaped(key))
		b.WriteByte('=')
		b.WriteString(encoded)
	}
	return b.String()
}

type renderPendingItem struct {
	span  StoredSpan
	depth int
}

// RenderTrace renders stored spans as a chronological forest, treating absent parents as roots.
// Lengths of time are milliseconds to the microsecond. Control characters in ids, names,
// attribute keys and error messages are escaped (attribute values are JSON).
func RenderTrace(spans []StoredSpan) string {
	byId := omap.NewMap[string, StoredSpan]()
	for _, span := range spans {
		byId.Set(span.Id, span)
	}

	sorted := byId.Values()
	slices.SortStableFunc(sorted, func(left, right StoredSpan) int {
		return cmp.Compare(left.StartedMs, right.StartedMs)
	})

	var roots []StoredSpan
	children := make(map[string][]StoredSpan)
	for _, span := range sorted {
		if span.ParentId == nil || !byId.Has(*span.ParentId) {
			roots = append(roots, span)
		} else {
			children[*span.ParentId] = append(children[*span.ParentId], span)
		}
	}

	var lines []string
	pending := make([]renderPendingItem, len(roots))
	for i, span := range roots {
		pending[len(roots)-1-i] = renderPendingItem{span: span, depth: 0}
	}

	for len(pending) > 0 {
		item := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		span := item.span
		indent := strings.Repeat("  ", item.depth)

		errText := ""
		if errStatus, ok := span.Status.(*SpanStatusError); ok && errStatus != nil {
			errText = " error"
			if errStatus.Error != nil {
				errText += " " + renderEscaped(errStatus.Error.Name) + ": " + renderEscaped(errStatus.Error.Message)
			}
		}

		duration := js.NumberToString(renderMicroseconds(span.EndedMs - span.StartedMs))
		lines = append(lines, fmt.Sprintf("%s%s%s (%sms)%s", indent, renderEscaped(span.Name), renderFormatAttributes(span.Attributes), duration, errText))

		for _, event := range span.Events {
			offset := js.NumberToString(renderMicroseconds(event.TimeMs))
			lines = append(lines, fmt.Sprintf("%s  · %s%s (+%sms)", indent, renderEscaped(event.Name), renderFormatAttributes(event.Attributes), offset))
		}
		for _, link := range span.Links {
			ids := "trace " + renderEscaped(link.TraceId) + " span " + renderEscaped(link.SpanId)
			lines = append(lines, fmt.Sprintf("%s  → link %s%s", indent, ids, renderFormatAttributes(link.Attributes)))
		}

		descendants := children[span.Id]
		for i := len(descendants) - 1; i >= 0; i-- {
			pending = append(pending, renderPendingItem{span: descendants[i], depth: item.depth + 1})
		}
	}
	return strings.Join(lines, "\n")
}
