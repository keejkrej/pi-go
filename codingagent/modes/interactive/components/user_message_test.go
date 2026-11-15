package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func TestUserMessageComponentOsc133AndOutputPad(t *testing.T) {
	comp := NewUserMessageComponent("hello user world")
	lines := comp.Render(50)
	if len(lines) < 3 {
		t.Fatalf("expected >= 3 lines (spacer + padded text), got %d", len(lines))
	}
	if !strings.HasPrefix(lines[0], "\x1b]133;A\x07") {
		t.Fatalf("expected OSC 133;A on first line, got %q", lines[0])
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, "\x1b]133;B\x07\x1b]133;C\x07") {
		t.Fatalf("expected OSC 133;B/C on last line, got %q", last)
	}
	plain := utils.StripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "hello user world") {
		t.Fatalf("expected prompt text in render, got %q", plain)
	}

	comp.SetOutputPad(4)
	padded := comp.Render(50)
	for i, l := range padded {
		if w := tui.VisibleWidth(l); w > 50 {
			t.Fatalf("line %d exceeded width 50 with outputPad: %d", i, w)
		}
	}

	called := false
	tr := NewUserMessageComponent("hi", &MessageComponentOptions{
		MarkdownTransformers: []core.MarkdownTransformer{
			func(md string, rctx core.MarkdownTransformContext) string {
				called = true
				if rctx.MessageType != core.MarkdownMessageTypeUser {
					t.Fatalf("expected user message type, got %s", rctx.MessageType)
				}
				return md + " transformed"
			},
		},
	})
	tPlain := utils.StripAnsi(strings.Join(tr.Render(60), "\n"))
	if !called || !strings.Contains(tPlain, "transformed") {
		t.Fatalf("expected markdown transformer applied in UserMessageComponent, got %q", tPlain)
	}
}
