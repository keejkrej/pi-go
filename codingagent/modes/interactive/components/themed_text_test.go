package components

import (
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
)

func TestThemedTextBuildAndInvalidate(t *testing.T) {
	calls := 0
	label := "initial"
	tt := NewThemedText(func() string {
		calls++
		return "hello " + label
	})

	lines := tt.Render(40)
	if calls != 1 {
		t.Fatalf("expected initial build call once, got %d", calls)
	}
	joined := utils.StripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "hello initial") {
		t.Fatalf("expected rendered text %q to contain 'hello initial'", joined)
	}

	// Default paddingX=1 and paddingY=1 means 3 lines for 1-line text.
	if len(lines) != 3 {
		t.Fatalf("expected default paddingY=1 -> 3 lines, got %d", len(lines))
	}

	label = "updated"
	tt.Invalidate()
	lines2 := tt.Render(40)
	if calls != 2 {
		t.Fatalf("expected Render after Invalidate to re-run build, got %d calls", calls)
	}
	joined2 := utils.StripAnsi(strings.Join(lines2, "\n"))
	if !strings.Contains(joined2, "hello updated") {
		t.Fatalf("expected updated text in %q", joined2)
	}
}

func TestThemedTextZeroPadding(t *testing.T) {
	tt := NewThemedText(func() string { return "compact" }, 0, 0)
	lines := tt.Render(40)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line with paddingY=0, got %d", len(lines))
	}
	if strings.TrimSpace(utils.StripAnsi(lines[0])) != "compact" {
		t.Fatalf("unexpected line content: %q", lines[0])
	}
}
