package components

import (
	"strings"
	"sync"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/core"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

type tsiMockTUI struct {
	tui.TUI
	mu      sync.Mutex
	renders int
}

func (m *tsiMockTUI) Post(fn func()) {
	if fn != nil {
		fn()
	}
}

func (m *tsiMockTUI) RequestRender(*bool) {
	m.mu.Lock()
	m.renders++
	m.mu.Unlock()
}

func TestStatusIndicatorKindsAndHeights(t *testing.T) {
	ui := &tsiMockTUI{}

	idle := NewIdleStatus()
	idleLines := idle.Render(40)
	if len(idleLines) != 2 {
		t.Fatalf("expected IdleStatus height 2, got %d", len(idleLines))
	}
	if tui.VisibleWidth(idleLines[0]) != 40 || tui.VisibleWidth(idleLines[1]) != 40 {
		t.Fatalf("expected IdleStatus lines padded to width 40")
	}

	customInterval := int64(50)
	working := NewWorkingStatusIndicator(ui, "Analyzing...", &core.WorkingIndicatorOptions{
		Frames:     []string{"*"},
		IntervalMs: &customInterval,
	})
	defer working.Dispose()

	if working.Kind() != StatusIndicatorKindWorking {
		t.Fatalf("expected kind working, got %q", working.Kind())
	}
	wLines := working.Render(60)
	if len(wLines) != 2 {
		t.Fatalf("expected standalone StatusIndicator height 2, got %d", len(wLines))
	}
	plainWorking := utils.StripAnsi(strings.Join(wLines, "\n"))
	if !strings.Contains(plainWorking, "Analyzing...") {
		t.Fatalf("expected message in rendered output, got %q", plainWorking)
	}

	inBorder := utils.StripAnsi(working.RenderInBorder(50))
	if !strings.Contains(inBorder, "Analyzing...") || !strings.Contains(inBorder, "*") {
		t.Fatalf("expected RenderInBorder to contain spinner & message, got %q", inBorder)
	}
	spinBorder := utils.StripAnsi(working.RenderSpinnerInBorder(50))
	if !strings.Contains(spinBorder, "*") {
		t.Fatalf("expected RenderSpinnerInBorder to contain spinner, got %q", spinBorder)
	}

	working.SetMessage("Finishing...")
	if !strings.Contains(utils.StripAnsi(working.RenderInBorder(50)), "Finishing...") {
		t.Fatalf("expected updated message in border render")
	}

	retry := NewRetryStatusIndicator(ui, 2, 5, 3000)
	defer retry.Dispose()
	if retry.Kind() != StatusIndicatorKindRetry {
		t.Fatalf("expected retry kind, got %q", retry.Kind())
	}
	if !strings.Contains(utils.StripAnsi(strings.Join(retry.Render(80), "\n")), "(2/5)") {
		t.Fatalf("expected retry attempt ratio in render")
	}

	comp := NewCompactionStatusIndicator(ui, CompactionStatusReasonOverflow)
	defer comp.Dispose()
	if comp.Kind() != StatusIndicatorKindCompaction {
		t.Fatalf("expected compaction kind, got %q", comp.Kind())
	}

	bs := NewBranchSummaryStatusIndicator(ui)
	defer bs.Dispose()
	if bs.Kind() != StatusIndicatorKindBranchSummary {
		t.Fatalf("expected branchSummary kind, got %q", bs.Kind())
	}
}
