package gui

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

func TestGUIRootViewAndOverlays(t *testing.T) {
	g := New(Options{
		Title:                "pi desktop",
		IncludeStatusInTitle: true,
	})
	g.SetRootView(func(c *ui.Context) {
		ui.Text(c, "main-workspace")
	})

	tt := ui.NewTester(g.View, 500, 400)
	if !tt.HasText("main-workspace") {
		t.Fatalf("expected root text, got %v", tt.Texts())
	}
	if g.HasOverlay() || g.OverlayCount() != 0 {
		t.Fatal("expected empty overlay stack initially")
	}

	var dismissed1, dismissed2 bool
	h1 := g.ShowOverlay(func(c *ui.Context) {
		ui.Text(c, "first-modal")
	}, OverlayOptions{
		OnDismiss: func() { dismissed1 = true },
	})
	_ = g.ShowOverlay(func(c *ui.Context) {
		ui.Text(c, "second-modal")
	}, OverlayOptions{
		OnDismiss: func() { dismissed2 = true },
	})

	tt.Frame()
	if !g.HasOverlay() || g.OverlayCount() != 2 {
		t.Fatalf("expected 2 overlays, got %d", g.OverlayCount())
	}
	if !tt.HasText("second-modal") {
		t.Fatalf("expected second-modal visible, got %v", tt.Texts())
	}

	// Escape dismisses ONLY top overlay (second-modal).
	tt.Key(0, ui.KeyEscape)
	if !dismissed2 || dismissed1 {
		t.Fatalf("expected only top overlay dismissed via Escape (d1=%v, d2=%v)", dismissed1, dismissed2)
	}
	if g.OverlayCount() != 1 || tt.HasText("second-modal") || !tt.HasText("first-modal") {
		t.Fatalf("expected first-modal to remain visible, texts=%v count=%d", tt.Texts(), g.OverlayCount())
	}

	// Clicking backdrop corner (outside centered panel) dismisses remaining overlay.
	tt.ClickAt(4, 4)
	if !dismissed1 || g.HasOverlay() {
		t.Fatalf("expected backdrop click to dismiss first-modal (d1=%v has=%v)", dismissed1, g.HasOverlay())
	}

	// SetHidden hides without destroying, and HideOverlay removes visible ones.
	h1 = g.ShowOverlay(func(c *ui.Context) {
		ui.Text(c, "reopened-modal")
	})
	tt.Frame()
	h1.SetHidden(true)
	tt.Frame()
	if g.HasOverlay() || tt.HasText("reopened-modal") {
		t.Fatal("expected hidden overlay not visible")
	}
	h1.SetHidden(false)
	tt.Frame()
	if !g.HasOverlay() || !tt.HasText("reopened-modal") {
		t.Fatal("expected unhidden overlay visible again")
	}
	if !g.HideOverlay() || g.HasOverlay() {
		t.Fatal("expected HideOverlay to clear overlay")
	}
}

func TestGUIOverlayDisableEscapeAndBackdrop(t *testing.T) {
	g := New()
	g.SetRootView(func(c *ui.Context) {
		ui.Text(c, "base")
	})
	tt := ui.NewTester(g.View, 400, 300)

	h := g.ShowOverlay(func(c *ui.Context) {
		ui.Text(c, "locked-modal")
	}, OverlayOptions{
		DisableEscape:        true,
		DisableBackdropClose: true,
	})
	tt.Frame()

	tt.Key(0, ui.KeyEscape)
	tt.ClickAt(2, 2)
	if !g.HasOverlay() || !tt.HasText("locked-modal") {
		t.Fatal("expected locked modal to ignore Escape and backdrop clicks")
	}
	h.Hide()
	tt.Frame()
	if g.HasOverlay() {
		t.Fatal("expected explicit h.Hide() to close locked modal")
	}
}

func TestGUIProgramStatusAndConcurrentPostRace(t *testing.T) {
	g := New(Options{
		Title:                "Pi",
		IncludeStatusInTitle: true,
	})

	var counter atomic.Int64
	g.SetRootView(func(c *ui.Context) {
		ui.Textf(c, "count=%d", counter.Load())
	})
	tt := ui.NewTester(g.View, 320, 200)

	g.SetProgramStatus(ProgramStatus{
		State:    StatusWorking,
		Message:  "Streaming",
		Progress: 0.4,
	})
	if eff := g.EffectiveTitle(); eff != "Pi — Streaming" {
		t.Fatalf("EffectiveTitle = %q, want %q", eff, "Pi — Streaming")
	}
	g.SetProgramStatus(ProgramStatus{State: StatusClear})
	if eff := g.EffectiveTitle(); eff != "Pi" {
		t.Fatalf("EffectiveTitle after clear = %q, want %q", eff, "Pi")
	}

	const workers = 12
	const perWorker = 25
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				g.Post(func() {
					counter.Add(1)
				})
				g.RequestRender()
			}
		}()
	}
	wg.Wait()

	tt.Frame()
	want := int64(workers * perWorker)
	if got := counter.Load(); got != want {
		t.Fatalf("counter = %d, want %d", got, want)
	}
	if !tt.HasText("count=300") {
		t.Fatalf("expected updated count=300 in view, got %v", tt.Texts())
	}
}

func TestGUIDesktopHelpersHeadlessAndHooks(t *testing.T) {
	g := New()
	g.SetRootView(func(c *ui.Context) {
		ui.Text(c, "desktop")
	})

	// Before Tester/App, dialogs/notify fail safely without panicking.
	if _, err := g.OpenFileDialog(mygo.OpenDialogOptions{}); !errors.Is(err, ErrDesktopUnavailable) {
		t.Fatalf("expected ErrDesktopUnavailable, got %v", err)
	}
	if err := g.Notify(mygo.NotificationOptions{Title: "Hi"}); !errors.Is(err, ErrDesktopUnavailable) {
		t.Fatalf("expected ErrDesktopUnavailable for Notify, got %v", err)
	}

	// Headless Tester services back clipboard text & PNG cleanly.
	tt := ui.NewTester(g.View, 300, 200)
	if err := g.WriteClipboardText("copied-hello"); err != nil {
		t.Fatalf("WriteClipboardText err: %v", err)
	}
	if got := g.ReadClipboardText(); got != "copied-hello" {
		t.Fatalf("ReadClipboardText = %q, want copied-hello", got)
	}

	pngPayload := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	tt.SetClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, pngPayload))))
	gotPNG, err := g.ReadClipboardPNG()
	if err != nil || len(gotPNG) != len(pngPayload) {
		t.Fatalf("ReadClipboardPNG = %v, err=%v", gotPNG, err)
	}

	// Custom DesktopHooks work seamlessly when installed.
	var notified string
	g.SetDesktopHooks(&DesktopHooks{
		OpenFileDialog: func(opts mygo.OpenDialogOptions) ([]string, error) {
			return []string{"/tmp/chosen.txt"}, nil
		},
		SaveFileDialog: func(opts mygo.SaveDialogOptions) (string, error) {
			return "/tmp/saved.txt", nil
		},
		MessageDialog: func(opts mygo.MessageOptions) (mygo.MessageResult, error) {
			return mygo.MessageResult{Button: 0}, nil
		},
		Notify: func(opts mygo.NotificationOptions) error {
			notified = opts.Title
			return nil
		},
	})
	paths, err := g.OpenFileDialog(mygo.OpenDialogOptions{Title: "Open"})
	if err != nil || len(paths) != 1 || paths[0] != "/tmp/chosen.txt" {
		t.Fatalf("hooked OpenFileDialog = %v, %v", paths, err)
	}
	savePath, err := g.SaveFileDialog(mygo.SaveDialogOptions{Title: "Save"})
	if err != nil || savePath != "/tmp/saved.txt" {
		t.Fatalf("hooked SaveFileDialog = %q, %v", savePath, err)
	}
	res, err := g.ShowMessageDialog(mygo.MessageOptions{Title: "Confirm"})
	if err != nil || res.Button != 0 {
		t.Fatalf("hooked MessageDialog = %+v, %v", res, err)
	}
	if err := g.Notify(mygo.NotificationOptions{Title: "Done"}); err != nil || notified != "Done" {
		t.Fatalf("hooked Notify err=%v notified=%q", err, notified)
	}
}
