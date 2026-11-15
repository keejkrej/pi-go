package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func TestDynamicBorderRender(t *testing.T) {
	b := NewDynamicBorder()
	lines := b.Render(12)
	if len(lines) != 1 {
		t.Fatalf("expected 1 border line, got %d", len(lines))
	}
	stripped := utils.StripAnsi(lines[0])
	if stripped != strings.Repeat("─", 12) {
		t.Fatalf("expected 12 box-drawing dashes, got %q", stripped)
	}
	if w := tui.VisibleWidth(lines[0]); w != 12 {
		t.Fatalf("expected visible width 12, got %d", w)
	}

	b.Invalidate()
	if len(b.Render(1)) != 1 || tui.VisibleWidth(b.Render(1)[0]) != 1 {
		t.Fatalf("expected width 1 to render single dash")
	}

	custom := NewDynamicBorder(func(s string) string { return "<" + s + ">" })
	customLines := custom.Render(4)
	if len(customLines) != 1 || customLines[0] != "<────>" {
		t.Fatalf("expected custom color callback applied, got %#v", customLines)
	}
}
