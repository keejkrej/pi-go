// Ported from packages/coding-agent/src/utils/tool-result-images.ts (pi v1.5.2).

package utils

import (
	"encoding/base64"
	"strings"

	"github.com/keejkrej/pi-go/ai"
)

// ToolResultContent aliases ai.Content blocks contained in tool results.
type ToolResultContent = ai.Content

// NormalizeToolResultImagesOptions configures tool result image normalization.
type NormalizeToolResultImagesOptions = ProcessImageOptions

// NormalizeToolResultImages converts non-LLM image formats to PNG and resizes oversized
// images in tool result content blocks, appending coordinate/conversion hint text blocks
// immediately after each modified image block. Returns the original content slice
// unchanged when no image block requires modification.
func NormalizeToolResultImages(content []ToolResultContent, options *NormalizeToolResultImagesOptions) []ToolResultContent {
	hasImage := false
	for _, block := range content {
		if _, ok := block.(*ai.ImageContent); ok {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return content
	}

	var out []ToolResultContent
	changed := false

	for idx, block := range content {
		img, ok := block.(*ai.ImageContent)
		if !ok || img == nil {
			if changed {
				out = append(out, block)
			}
			continue
		}

		raw, err := base64.StdEncoding.DecodeString(img.Data)
		if err != nil {
			if changed {
				out = append(out, block)
			}
			continue
		}

		res := ProcessImage(raw, img.MimeType, options)
		if !res.Ok {
			// Keep undecodable images intact instead of dropping tool output.
			if changed {
				out = append(out, block)
			}
			continue
		}

		if res.Data == img.Data && res.MimeType == img.MimeType && len(res.Hints) == 0 {
			if changed {
				out = append(out, block)
			}
			continue
		}

		if !changed {
			changed = true
			out = make([]ToolResultContent, 0, len(content)+len(res.Hints))
			out = append(out, content[:idx]...)
		}

		out = append(out, &ai.ImageContent{
			Type:     "image",
			Data:     res.Data,
			MimeType: res.MimeType,
		})
		if len(res.Hints) > 0 {
			out = append(out, &ai.TextContent{
				Type: "text",
				Text: strings.Join(res.Hints, "\n"),
			})
		}
	}

	if !changed {
		return content
	}
	return out
}
