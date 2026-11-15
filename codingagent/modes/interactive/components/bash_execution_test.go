package components

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

type tbeMockTUI struct {
	tui.TUI
	mu      sync.Mutex
	renders int
}

func (m *tbeMockTUI) Post(fn func()) {
	if fn != nil {
		fn()
	}
}

func (m *tbeMockTUI) RequestRender(*bool) {
	m.mu.Lock()
	m.renders++
	m.mu.Unlock()
}

func TestBashExecutionComponentStreamingCollapseExpandAndWidth(t *testing.T) {
	ui := &tbeMockTUI{}
	comp := NewBashExecutionComponent("echo hi", ui, false)
	defer comp.Dispose()

	if comp.GetCommand() != "echo hi" {
		t.Fatalf("unexpected GetCommand %q", comp.GetCommand())
	}

	var b strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&b, "line %02d with some wide content to wrap cleanly\n", i)
	}
	comp.AppendOutput(b.String())
	if !strings.Contains(comp.GetOutput(), "line 30") {
		t.Fatalf("expected appended output stored")
	}

	exitZero := 0
	comp.SetComplete(&exitZero, false, nil, nil)

	// Collapsed preview clamps to width and shows expand hint.
	for _, w := range []int{40, 80} {
		lines := comp.Render(w)
		for i, l := range lines {
			if vw := tui.VisibleWidth(l); vw > w {
				t.Fatalf("width %d line %d overflowed: %d", w, i, vw)
			}
		}
		plain := utils.StripAnsi(strings.Join(lines, "\n"))
		if !strings.Contains(plain, "line 30") || !strings.Contains(plain, "earlier lines") {
			t.Fatalf("width %d collapsed view missing tail/truncation note: %q", w, plain)
		}
	}

	// Expanded view shows early lines too.
	comp.SetExpanded(true)
	expandedPlain := utils.StripAnsi(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(expandedPlain, "line 01") || !strings.Contains(expandedPlain, "line 30") {
		t.Fatalf("expected expanded view to show all lines, got %q", expandedPlain)
	}

	comp.SetOutputPad(3)
	for i, l := range comp.Render(45) {
		if tui.VisibleWidth(l) > 45 {
			t.Fatalf("padded line %d overflowed width 45: %d", i, tui.VisibleWidth(l))
		}
	}
}
