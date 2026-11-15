package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

func TestEarendilAnnouncementRender(t *testing.T) {
	c := NewEarendilAnnouncementComponent()
	lines := c.Render(80)
	if len(lines) == 0 {
		t.Fatalf("expected non-empty announcement lines")
	}
	plain := utils.StripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "Earendil") {
		t.Fatalf("expected announcement to mention Earendil, got %q", plain)
	}
	for i, line := range lines {
		if w := tui.VisibleWidth(line); w > 80 {
			t.Fatalf("line %d exceeded terminal width 80: %d", i, w)
		}
	}
}
