// Ported from packages/coding-agent/src/cli/initial-message.ts (pi v1.5.2).

package cli

import (
	"strings"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/core"
)

// InitialMessageInput supplies stdin, `@file` expansions, and CLI positional messages.
type InitialMessageInput struct {
	Parsed       *core.Args
	FileText     *string
	FileImages   []*ai.ImageContent
	StdinContent *string
}

// InitialMessageResult contains the assembled initial prompt text and images.
type InitialMessageResult struct {
	InitialMessage *string
	InitialImages  []*ai.ImageContent
}

// BuildInitialMessage combines stdin content, `@file` text, and the first CLI
// positional message into one initial prompt, consuming that first message from input.Parsed.
func BuildInitialMessage(input InitialMessageInput) InitialMessageResult {
	var parts []string
	if input.StdinContent != nil {
		parts = append(parts, *input.StdinContent)
	}
	if input.FileText != nil && *input.FileText != "" {
		parts = append(parts, *input.FileText)
	}
	if input.Parsed != nil && len(input.Parsed.Messages) > 0 {
		parts = append(parts, input.Parsed.Messages[0])
		input.Parsed.Messages = input.Parsed.Messages[1:]
	}

	var initialMessage *string
	if len(parts) > 0 {
		joined := strings.Join(parts, "")
		initialMessage = &joined
	}

	var initialImages []*ai.ImageContent
	if len(input.FileImages) > 0 {
		initialImages = input.FileImages
	}

	return InitialMessageResult{
		InitialMessage: initialMessage,
		InitialImages:  initialImages,
	}
}
