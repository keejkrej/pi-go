package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
)

func TestMermaidMarkdownTransformerModesAndRendering(t *testing.T) {
	mode := core.MermaidRenderingFinal
	tr := CreateMermaidMarkdownTransformer(MermaidTransformerOptions{
		GetMode: func() core.MermaidRenderingMode { return mode },
		Theme:   theme.CurrentTheme(),
	})

	input := "# Heading\n\n```mermaid\ngraph LR\n  A --> B\n```\n"

	// Unsupported message types should pass through unchanged.
	for _, mt := range []core.MarkdownMessageType{
		core.MarkdownMessageTypeAssistantThinking,
		core.MarkdownMessageTypeCustom,
		core.MarkdownMessageTypeChangelog,
	} {
		out := tr(input, core.MarkdownTransformContext{
			MessageType:    mt,
			IsStreaming:    false,
			AvailableWidth: 80,
		})
		if out != input {
			t.Fatalf("expected %s to skip mermaid rendering, got %q", mt, out)
		}
	}

	// Off mode returns input unchanged.
	mode = core.MermaidRenderingOff
	if out := tr(input, core.MarkdownTransformContext{
		MessageType:    core.MarkdownMessageTypeAssistant,
		IsStreaming:    false,
		AvailableWidth: 80,
	}); out != input {
		t.Fatalf("expected off mode to pass through")
	}

	// Final mode leaves streaming untouched, renders when done.
	mode = core.MermaidRenderingFinal
	if out := tr(input, core.MarkdownTransformContext{
		MessageType:    core.MarkdownMessageTypeAssistant,
		IsStreaming:    true,
		AvailableWidth: 80,
	}); out != input {
		t.Fatalf("expected final mode during streaming to defer rendering")
	}

	finalOut := tr(input, core.MarkdownTransformContext{
		MessageType:    core.MarkdownMessageTypeAssistant,
		IsStreaming:    false,
		AvailableWidth: 80,
	})
	plainFinal := utils.StripAnsi(finalOut)
	if strings.Contains(plainFinal, "```mermaid") {
		t.Fatalf("expected rendered final output to replace mermaid fence, got %q", plainFinal)
	}
	if !strings.Contains(plainFinal, "A") || !strings.Contains(plainFinal, "B") {
		t.Fatalf("expected rendered diagram labels A and B, got %q", plainFinal)
	}

	// Streaming mode renders even while streaming.
	mode = core.MermaidRenderingStreaming
	streamOut := tr(input, core.MarkdownTransformContext{
		MessageType:    core.MarkdownMessageTypeAssistant,
		IsStreaming:    true,
		AvailableWidth: 80,
	})
	if streamOut == input {
		t.Fatalf("expected streaming mode to render immediately during stream")
	}
}

func TestMermaidMarkdownTransformerInvalidDiagramFallback(t *testing.T) {
	tr := CreateMermaidMarkdownTransformer(MermaidTransformerOptions{
		GetMode: func() core.MermaidRenderingMode { return core.MermaidRenderingFinal },
	})
	bad := "```mermaid\ncompletely invalid diagram syntax ??? !!!\n```\n"

	// Final failed diagram falls back gracefully (never panics, includes note or keeps source).
	out := tr(bad, core.MarkdownTransformContext{
		MessageType:    core.MarkdownMessageTypeAssistant,
		IsStreaming:    false,
		AvailableWidth: 60,
	})
	if strings.TrimSpace(out) == "" {
		t.Fatalf("expected non-empty fallback output on parse warning/error")
	}
}
