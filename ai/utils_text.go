// Ported from packages/ai/src/utils/text.ts (pi v1.5.2).

package ai

import (
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
)

func utContentBlocksText(blocks ContentList, sep string) string {
	var parts []string
	for _, b := range blocks {
		if tc, ok := b.(*TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, sep)
}

// ContentText extracts and joins text from message content.
// Accepts string, *string, SystemMessageContent, UserMessageContent, ContentList, or []Content.
func ContentText(content any, separator ...string) string {
	sep := "\n"
	if len(separator) > 0 {
		sep = separator[0]
	}
	switch v := content.(type) {
	case nil:
		return ""
	case string:
		return v
	case *string:
		if v == nil {
			return ""
		}
		return *v
	case SystemMessageContent:
		if v.Text != nil {
			return *v.Text
		}
		return utContentBlocksText(v.Blocks, sep)
	case *SystemMessageContent:
		if v == nil {
			return ""
		}
		if v.Text != nil {
			return *v.Text
		}
		return utContentBlocksText(v.Blocks, sep)
	case UserMessageContent:
		if v.Text != nil {
			return *v.Text
		}
		return utContentBlocksText(v.Blocks, sep)
	case *UserMessageContent:
		if v == nil {
			return ""
		}
		if v.Text != nil {
			return *v.Text
		}
		return utContentBlocksText(v.Blocks, sep)
	case ContentList:
		return utContentBlocksText(v, sep)
	case []Content:
		return utContentBlocksText(ContentList(v), sep)
	default:
		return ""
	}
}

// GetSystemMessageText renders a system message as a complete prompt: its content followed by its sections.
func GetSystemMessageText(message *SystemMessage) string {
	if message == nil {
		return ""
	}
	parts := make([]string, 0, 1)
	if text := ContentText(message.Content); len(text) > 0 {
		parts = append(parts, text)
	}
	if message.Sections != nil {
		for _, text := range message.Sections.Values() {
			if text != nil && len(*text) > 0 {
				parts = append(parts, *text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// RenderSystemMessageUpdate renders a later system message for APIs that accept system messages mid-conversation.
func RenderSystemMessageUpdate(message *SystemMessage) string {
	if message == nil {
		return ""
	}
	var parts []string
	if text := ContentText(message.Content); len(text) > 0 {
		parts = append(parts, text)
	}
	if message.Sections != nil {
		for name, value := range message.Sections.All() {
			if value == nil {
				parts = append(parts, `Removed system prompt section "`+name+`".`)
			} else {
				parts = append(parts, `Updated system prompt section "`+name+`":`+"\n\n"+*value)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// OwnedSlice returns a standalone copy of text.slice(start, end) using UTF-16 code unit indices.
func OwnedSlice(text string, start int, end ...int) string {
	if len(end) > 0 {
		return strings.Clone(js.Slice(text, start, end[0]))
	}
	return strings.Clone(js.SliceFrom(text, start))
}
