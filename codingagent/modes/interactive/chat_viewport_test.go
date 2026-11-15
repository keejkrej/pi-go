// Ported from packages/coding-agent/test/chat-viewport.test.ts (pi v1.5.2).

package interactive

import (
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/tui"
)

type tcvFixedComponent struct {
	lines []string
}

func (c *tcvFixedComponent) Render(_ int) []string {
	return slices.Clone(c.lines)
}

func (c *tcvFixedComponent) Invalidate() {}

func tcvLine(width int, ch string) string {
	if width <= 0 {
		return ""
	}
	return strings.Repeat(ch, width)
}

func TestChatViewportFixedDockAndScrollableTranscript(t *testing.T) {
	tithInitTestTheme(t)

	doc := &tcvFixedComponent{lines: func() []string {
		out := make([]string, 30)
		for i := range out {
			out[i] = tcvLine(10, "d")
		}
		return out
	}()}
	pending := &tcvFixedComponent{lines: []string{"pending-1"}}
	status := &tcvFixedComponent{lines: []string{"status-1"}}
	above := &tcvFixedComponent{lines: []string{"above-1"}}
	editor := &tcvFixedComponent{lines: []string{"editor-1", "editor-2", "editor-3"}}
	below := &tcvFixedComponent{lines: []string{"below-1"}}
	footer := &tcvFixedComponent{lines: []string{"footer-1"}}

	scrollbar := tui.ScrollViewScrollbarAlways
	vp := CreateChatViewport(ChatViewportOptions{
		Document:        doc,
		PendingMessages: pending,
		Status:          status,
		WidgetsAbove:    above,
		Editor:          editor,
		WidgetsBelow:    below,
		Footer:          footer,
		Scrollbar:       &scrollbar,
		ScrollbarTrackStyle: func(text string) string {
			return "[" + text + "]"
		},
		ScrollbarThumbStyle: func(text string) string {
			return "<" + text + ">"
		},
	})
	if vp == nil || vp.Root == nil || vp.Transcript == nil {
		t.Fatalf("CreateChatViewport returned nil fields: %+v", vp)
	}

	frame := tui.RenderLayoutFrame(vp.Root, 40, 14, nil)
	lines := frame.Lines
	if len(lines) != 14 {
		t.Fatalf("expected 14 rendered rows, got %d", len(lines))
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"pending-1", "status-1", "above-1", "editor-1", "below-1", "footer-1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected dock row %q in rendered viewport:\n%s", want, joined)
		}
	}
}

func TestChatViewportOptionalWidgetsOmitted(t *testing.T) {
	vp := CreateChatViewport(ChatViewportOptions{
		Document:        &tcvFixedComponent{lines: []string{"msg-1", "msg-2"}},
		PendingMessages: &tcvFixedComponent{},
		Status:          &tcvFixedComponent{},
		Editor:          &tcvFixedComponent{lines: []string{"e1", "e2", "e3"}},
		Footer:          &tcvFixedComponent{lines: []string{"f1"}},
	})
	frame := tui.RenderLayoutFrame(vp.Root, 30, 8, nil)
	lines := frame.Lines
	if len(lines) != 8 {
		t.Fatalf("expected 8 rows, got %d", len(lines))
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "msg-1") || !strings.Contains(joined, "f1") {
		t.Fatalf("expected transcript and footer in output:\n%s", joined)
	}
}
