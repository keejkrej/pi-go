package components

import (
	"errors"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/tui"
)

type tauMockTerm struct {
	tui.Terminal
	columns int
	rows    int
}

func (m tauMockTerm) Columns() int { return m.columns }
func (m tauMockTerm) Rows() int    { return m.rows }

type tauMockTUI struct {
	tui.TUI
	term tauMockTerm
}

func (m *tauMockTUI) Terminal() tui.Terminal { return m.term }
func (m *tauMockTUI) Post(fn func()) {
	if fn != nil {
		fn()
	}
}
func (m *tauMockTUI) RequestRender(*bool) {}

func TestAuthUrlComponentRenderAndCopy(t *testing.T) {
	origCopy := auCopyToClipboard
	t.Cleanup(func() { auCopyToClipboard = origCopy })

	var copied string
	auCopyToClipboard = func(text string) error {
		copied = text
		return nil
	}

	ui := &tauMockTUI{term: tauMockTerm{columns: 100, rows: 30}}
	targetURL := "https://auth.example.com/oauth?code=123"
	comp := NewAuthUrlComponent(ui, targetURL)

	if comp.Url() != targetURL {
		t.Fatalf("Url() = %q, want %q", comp.Url(), targetURL)
	}

	plain := utils.StripAnsi(strings.Join(comp.Render(100), "\n"))
	if !strings.Contains(plain, targetURL) {
		t.Fatalf("expected rendered component to show URL %q, got %q", targetURL, plain)
	}

	if err := comp.Copy(); err != nil {
		t.Fatalf("Copy() failed: %v", err)
	}
	if copied != targetURL {
		t.Fatalf("expected clipboard payload %q, got %q", targetURL, copied)
	}

	auCopyToClipboard = func(string) error {
		return errors.New("clipboard unavailable")
	}
	if err := comp.Copy(); err == nil {
		t.Fatalf("expected clipboard error propagation")
	}
}
