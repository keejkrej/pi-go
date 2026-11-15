package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func TestAssistantMessageComponentThinkingCoalesceToggleAndErrors(t *testing.T) {
	msg := &ai.AssistantMessage{
		Role: "assistant",
		Content: ai.ContentList{
			&ai.ThinkingContent{Type: "thinking", Thinking: "first thought"},
			&ai.ThinkingContent{Type: "thinking", Thinking: "second thought"},
			&ai.TextContent{Type: "text", Text: "final answer"},
		},
		StopReason: ai.StopReasonStop,
	}

	hiddenLabel := "Thinking collapsed"
	comp := NewAssistantMessageComponent(msg, &AssistantMessageOptions{
		HiddenThinkingLabel: &hiddenLabel,
	})

	// Visible thinking by default coalesces adjacent thinking blocks.
	plainVisible := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(plainVisible, "first thought") || !strings.Contains(plainVisible, "second thought") {
		t.Fatalf("expected coalesced thinking text, got %q", plainVisible)
	}
	if !strings.Contains(plainVisible, "final answer") {
		t.Fatalf("expected answer text, got %q", plainVisible)
	}
	if strings.Contains(plainVisible, "Thinking collapsed") {
		t.Fatalf("unexpected collapsed label when thinking visible")
	}

	// Collapsing via setter shows label and hides thinking body.
	comp.SetHideThinkingBlock(true)
	plainHidden := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if strings.Contains(plainHidden, "first thought") || !strings.Contains(plainHidden, "Thinking collapsed") {
		t.Fatalf("expected collapsed label instead of thinking body, got %q", plainHidden)
	}

	// Clicking row 1 toggles thinking visibility back on.
	res := comp.HandleMouse(tui.TuiMouseEvent{
		Type:   tui.TuiMouseEventTypeClick,
		Button: tui.TuiMouseButtonLeft,
		X:      2,
		Y:      1,
		Width:  80,
		Height: len(comp.Render(80)),
	})
	if res == nil || !res.Handled {
		t.Fatalf("expected left click on thinking block to be handled")
	}
	plainClicked := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(plainClicked, "first thought") {
		t.Fatalf("expected click to uncollapse thinking block, got %q", plainClicked)
	}

	// Error / aborted stop reasons when no tool calls.
	errMsgText := "rate limited"
	msgErr := &ai.AssistantMessage{
		Role:         "assistant",
		Content:      ai.ContentList{},
		StopReason:   ai.StopReasonError,
		ErrorMessage: &errMsgText,
	}
	comp.UpdateContent(msgErr, false)
	plainErr := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(plainErr, "rate limited") {
		t.Fatalf("expected stop error message, got %q", plainErr)
	}

	// Length stop reason.
	comp.UpdateContent(&ai.AssistantMessage{
		Role:       "assistant",
		Content:    ai.ContentList{&ai.TextContent{Type: "text", Text: "partial"}},
		StopReason: ai.StopReasonLength,
	}, false)
	linesWithOsc := comp.Render(80)
	joinedOsc := strings.Join(linesWithOsc, "\n")
	if !strings.Contains(joinedOsc, "\x1b]133;A\x07") || !strings.Contains(joinedOsc, "\x1b]133;B\x07\x1b]133;C\x07") {
		t.Fatalf("expected OSC 133 A/B/C markers when finished without tool calls")
	}
}
