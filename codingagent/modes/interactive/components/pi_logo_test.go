package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func TestPiLogoLinesAndWordmark(t *testing.T) {
	lines := PiLogoLines()
	if len(lines) == 0 {
		t.Fatalf("expected non-empty Pi logo lines")
	}
	w0 := tui.VisibleWidth(lines[0])
	if w0 <= 0 {
		t.Fatalf("expected positive visible width, got %d", w0)
	}
	for i, l := range lines {
		if got := tui.VisibleWidth(l); got != w0 {
			t.Fatalf("line %d width %d != line 0 width %d", i, got, w0)
		}
	}

	wm := PiWordmark()
	if strings.TrimSpace(utils.StripAnsi(wm)) == "" {
		t.Fatalf("expected non-empty PiWordmark")
	}

	_ = SupportsPiLogo()
}
