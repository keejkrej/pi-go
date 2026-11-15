package components

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/modes/interactive/theme"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

type tblMockTUI struct {
	tui.TUI
	renders atomic.Int32
}

func (m *tblMockTUI) Post(fn func()) {
	if fn != nil {
		fn()
	}
}

func (m *tblMockTUI) RequestRender(*bool) {
	m.renders.Add(1)
}

func TestBorderedLoaderCancellableAndNonCancellable(t *testing.T) {
	ui := &tblMockTUI{}
	th := theme.CurrentTheme()

	loader := NewBorderedLoader(ui, th, "Loading models...")
	defer loader.Dispose()

	lines := loader.Render(60)
	if len(lines) < 3 {
		t.Fatalf("expected bordered loader lines, got %d", len(lines))
	}
	plain := utils.StripAnsi(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "Loading models...") {
		t.Fatalf("expected message in rendered lines, got %q", plain)
	}

	aborted := 0
	loader.OnAbort = func() { aborted++ }
	loader.HandleInput("\x1b") // escape
	if aborted != 1 {
		t.Fatalf("expected OnAbort once on escape, got %d", aborted)
	}
	select {
	case <-loader.Signal().Done():
	default:
		t.Fatalf("expected Signal() context cancelled on abort")
	}

	cancellable := false
	nonCancel := NewBorderedLoader(ui, th, "Working...", &BorderedLoaderOptions{Cancellable: &cancellable})
	defer nonCancel.Dispose()

	nonCancelAborted := 0
	nonCancel.OnAbort = func() { nonCancelAborted++ }
	nonCancel.HandleInput("\x1b")
	if nonCancelAborted != 0 {
		t.Fatalf("expected non-cancellable loader to ignore escape")
	}
	select {
	case <-nonCancel.Signal().Done():
		t.Fatalf("expected non-cancellable Signal() to remain active until Dispose")
	default:
	}

	nonCancel.Dispose()
	select {
	case <-nonCancel.Signal().Done():
	default:
		t.Fatalf("expected Dispose() to cancel Signal()")
	}
}
