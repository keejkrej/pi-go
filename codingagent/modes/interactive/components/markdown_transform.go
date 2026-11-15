// Ported from packages/coding-agent/src/modes/interactive/components/markdown-transform.ts (pi v1.5.2).

package components

import "github.com/keejkrej/pi-go/codingagent/core"

// CreateMarkdownTransform returns a transform function compatible with tui.MarkdownOptions.Transform
// that applies the registered MarkdownTransformers sequentially, continuing past panics.
// Returns nil when transformers is empty.
func CreateMarkdownTransform(
	messageType core.MarkdownMessageType,
	isStreaming func() bool,
	transformers []core.MarkdownTransformer,
	messageId, timestamp *string,
) func(markdown string, availableWidth int) string {
	if len(transformers) == 0 {
		return nil
	}
	chain := append([]core.MarkdownTransformer(nil), transformers...)
	return func(markdown string, availableWidth int) string {
		streaming := false
		if isStreaming != nil {
			streaming = isStreaming()
		}
		ctx := core.MarkdownTransformContext{
			MessageType:    messageType,
			IsStreaming:    streaming,
			AvailableWidth: availableWidth,
			MessageId:      messageId,
			Timestamp:      timestamp,
		}
		if messageId == nil {
			ctx.MessageId = nil
		}
		if timestamp == nil {
			ctx.Timestamp = nil
		}
		current := markdown
		for _, transformer := range chain {
			if transformer == nil {
				continue
			}
			current = mtSafeApply(transformer, current, ctx)
		}
		return current
	}
}

func mtSafeApply(transformer core.MarkdownTransformer, markdown string, ctx core.MarkdownTransformContext) (out string) {
	out = markdown
	defer func() {
		if recover() != nil {
			out = markdown
		}
	}()
	return transformer(markdown, ctx)
}
